package datatransfer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/looplj/axonhub/internal/server/biz"
)

var databaseName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)
var toolVersion = regexp.MustCompile(`PostgreSQL\) (\d+)`)

func (s *Service) connect(ctx context.Context, database string) (*pgx.Conn, error) {
	cfg, err := pgx.ParseConfig(s.dbConfig.DSN)
	if err != nil {
		return nil, ErrUnsupported
	}
	cfg.ConnectTimeout = 5 * time.Second
	if database != "" {
		cfg.Database = database
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, ErrProcess
	}
	return conn, nil
}

// Native tools must use the same URL options (TLS, certificates, socket, etc.)
// as the application. Passwords are moved to the child environment, never argv.
func (s *Service) command(ctx context.Context, binary, database, user, password string, args ...string) (*exec.Cmd, error) {
	u, err := url.Parse(s.dbConfig.DSN)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return nil, ErrUnsupported
	}
	cfg, err := pgx.ParseConfig(s.dbConfig.DSN)
	if err != nil {
		return nil, ErrUnsupported
	}
	q := u.Query()
	if q.Get("sslpassword") != "" {
		return nil, ErrUnsupported
	}
	q.Del("password")
	q.Del("user")
	q.Del("dbname")
	q.Set("target_session_attrs", "read-write")
	if database == "" {
		database = cfg.Database
	}
	if user == "" {
		user, password = cfg.User, cfg.Password
	}
	u.User = url.User(user)
	u.Path = "/" + database
	u.RawPath = ""
	u.RawQuery = q.Encode()
	cmd := exec.CommandContext(ctx, binary, append([]string{"--no-password", "--dbname", u.String()}, args...)...)
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		if strings.HasPrefix(strings.ToUpper(name), "PG") {
			continue
		}
		cmd.Env = append(cmd.Env, value)
	}
	cmd.Env = append(cmd.Env, "PGPASSWORD="+password, "PGCONNECT_TIMEOUT=5", "PGAPPNAME=axonhub-migration", "PGOPTIONS=-c work_mem=16MB -c maintenance_work_mem=64MB")
	cmd.Stderr = io.Discard // Tool messages may contain row data or connection details.
	cmd.WaitDelay = 3 * time.Second
	return cmd, nil
}

func (s *Service) checkTools(ctx context.Context) (int, error) {
	if s.dbConfig.Dialect != "postgres" && s.dbConfig.Dialect != "postgresql" {
		return 0, ErrUnsupported
	}
	if _, err := s.command(ctx, "pg_dump", "", "", "", "--version"); err != nil {
		return 0, err
	}
	conn, err := s.connect(ctx, "")
	if err != nil {
		return 0, err
	}
	defer conn.Close(context.Background())
	var version int
	if err := conn.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer").Scan(&version); err != nil {
		return 0, ErrProcess
	}
	major := version / 10000
	for _, binary := range []string{"pg_dump", "pg_restore"} {
		childCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		out, err := exec.CommandContext(childCtx, binary, "--version").Output()
		cancel()
		match := toolVersion.FindSubmatch(out)
		if err != nil || len(match) != 2 {
			return 0, ErrTools
		}
		toolMajor, _ := strconv.Atoi(string(match[1]))
		// Matching major versions make exported files predictable on another
		// deployment of the same supported server version.
		if toolMajor != major {
			return 0, ErrVersion
		}
	}
	return major, nil
}

func (s *Service) importPermission(ctx context.Context) error {
	conn, err := s.connect(ctx, "")
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	var allowed bool
	if err := conn.QueryRow(ctx, "SELECT rolsuper OR (rolcreatedb AND rolcreaterole) FROM pg_roles WHERE rolname=current_user").Scan(&allowed); err != nil || !allowed {
		return ErrRole
	}
	return nil
}

func (s *Service) dump(ctx context.Context, out io.Writer) (int64, error) {
	conn, err := s.connect(ctx, "")
	if err != nil {
		return 0, err
	}
	var size int64
	err = conn.QueryRow(ctx, "SELECT pg_database_size(current_database())").Scan(&size)
	conn.Close(context.Background())
	if err != nil {
		return 0, ErrProcess
	}
	cmd, err := s.command(ctx, "pg_dump", "", "", "", "--format=custom", "--compress=3", "--no-owner", "--no-acl", "--lock-wait-timeout=3s")
	if err != nil {
		return 0, err
	}
	cmd.Stdout = out
	if err := runCommand(cmd); err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, ErrProcess
	}
	return size, nil
}

func roleName(id string) string { return "axonhub_import_" + strings.ReplaceAll(id, "-", "") }

func (s *Service) restore(ctx context.Context, job *Job, dump *os.File) (result error) {
	if !databaseName.MatchString(job.TargetDatabase) {
		return ErrTarget
	}
	conn, err := s.connect(ctx, "")
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", job.TargetDatabase).Scan(&exists); err != nil {
		return ErrProcess
	}
	if exists {
		return ErrTarget
	}
	var roleOwner string
	if err := conn.QueryRow(ctx, "SELECT current_user").Scan(&roleOwner); err != nil {
		return ErrProcess
	}
	role := roleName(job.ID)
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return err
	}
	password := hex.EncodeToString(secret[:])
	identifier := pgx.Identifier{role}.Sanitize()
	// Authenticate pg_restore as the restricted role. SET ROLE on a privileged
	// connection would allow an archive to RESET ROLE and escape the boundary.
	tx, err := conn.Begin(ctx)
	if err != nil {
		return ErrProcess
	}
	defer tx.Rollback(context.Background())
	_, err = tx.Exec(ctx, fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS VALID UNTIL '%s'", identifier, password, time.Now().UTC().Add(time.Duration(s.config.TimeoutSeconds+300)*time.Second).Format(time.RFC3339)))
	if err != nil {
		return ErrRole
	}
	if _, err = tx.Exec(ctx, "COMMENT ON ROLE "+identifier+" IS '"+job.ID+"'"); err != nil {
		return ErrRole
	}
	if _, err = tx.Exec(ctx, "GRANT "+identifier+" TO "+pgx.Identifier{roleOwner}.Sanitize()); err != nil {
		return ErrRole
	}
	if err = tx.Commit(ctx); err != nil {
		return ErrRole
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if cleanupErr := s.cleanupRestoreRole(cleanupCtx, job); cleanupErr != nil && result == nil {
			result = ErrRole
		}
	}()
	_, err = conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{job.TargetDatabase}.Sanitize()+" WITH TEMPLATE template0 OWNER "+identifier)
	if err != nil {
		return ErrTarget
	}
	cmd, err := s.command(ctx, "pg_restore", job.TargetDatabase, role, password, "--single-transaction", "--exit-on-error", "--no-owner", "--no-acl")
	if err != nil {
		return err
	}
	cmd.Stdin = dump
	cmd.Stdout = io.Discard
	if err = runCommand(cmd); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrProcess
	}
	return s.prepareImportedDatabase(ctx, job.TargetDatabase, role, password)
}

func (s *Service) cleanupRestoreRole(ctx context.Context, job *Job) error {
	conn, err := s.connect(ctx, "")
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	role := roleName(job.ID)
	var owned bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1 AND shobj_description(oid,'pg_authid')=$2)", role, job.ID).Scan(&owned); err != nil {
		return ErrProcess
	}
	if !owned {
		return nil
	}
	identifier := pgx.Identifier{role}.Sanitize()
	if _, err := conn.Exec(ctx, "ALTER ROLE "+identifier+" NOLOGIN"); err != nil {
		return ErrRole
	}
	var targetExists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", job.TargetDatabase).Scan(&targetExists); err != nil {
		return ErrProcess
	}
	if targetExists {
		target, err := s.connect(ctx, job.TargetDatabase)
		if err != nil {
			return err
		}
		_, err = target.Exec(ctx, "REASSIGN OWNED BY "+identifier+" TO CURRENT_USER")
		if err == nil {
			_, err = target.Exec(ctx, "DROP OWNED BY "+identifier)
		}
		target.Close(context.Background())
		if err != nil {
			return ErrRole
		}
	}
	_, err = conn.Exec(ctx, "DROP ROLE "+identifier)
	if err != nil {
		return ErrRole
	}
	return nil
}

func (s *Service) prepareImportedDatabase(ctx context.Context, name, role, password string) error {
	// Imported triggers must not execute with the application's privileged role.
	cfg, err := pgx.ParseConfig(s.dbConfig.DSN)
	if err != nil {
		return ErrUnsupported
	}
	cfg.Database, cfg.User, cfg.Password = name, role, password
	cfg.ConnectTimeout = 5 * time.Second
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	tx, err := conn.Begin(ctx)
	if err != nil {
		return ErrProcess
	}
	defer tx.Rollback(context.Background())
	// Runtime jobs and login signatures belong to the target installation.
	if _, err := tx.Exec(ctx, "DELETE FROM systems WHERE key IN ($1,$2)", jobKey, "maintenance_cleanup_task_v1"); err != nil {
		return ErrPackage
	}
	secret, err := biz.GenerateSecretKey()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "UPDATE systems SET value=$1 WHERE key=$2", secret, biz.SystemKeySecretKey); err != nil {
		return ErrProcess
	}
	if _, err := tx.Exec(ctx, "UPDATE systems SET value=jsonb_set(value::jsonb,'{enabled}','false')::text WHERE key=$1", biz.SystemKeyAutoBackupSettings); err != nil {
		return ErrProcess
	}
	if err := tx.Commit(ctx); err != nil {
		return ErrProcess
	}
	return nil
}
