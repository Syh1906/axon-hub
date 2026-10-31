package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/user"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/internal/server/datatransfer"
	"go.uber.org/fx"
)

type DatabaseTransferHandlers struct {
	service   *datatransfer.Service
	client    *ent.Client
	system    *biz.SystemService
	publicURL string
}

type DatabaseTransferParams struct {
	fx.In
	Service   *datatransfer.Service
	Client    *ent.Client
	System    *biz.SystemService
	PublicURL string `name:"public_url"`
}

func NewDatabaseTransferHandlers(p DatabaseTransferParams) *DatabaseTransferHandlers {
	return &DatabaseTransferHandlers{service: p.Service, client: p.Client, system: p.System, publicURL: p.PublicURL}
}

func (h *DatabaseTransferHandlers) isOwner(ctx context.Context, id int) bool {
	tx, err := h.client.Tx(authz.WithSystemBypass(ctx, "migration-owner-check"))
	if err != nil {
		return false
	}
	defer tx.Rollback()
	found, err := tx.User.Query().Where(user.IDEQ(id), user.IsOwnerEQ(true), user.StatusEQ(user.StatusActivated)).Exist(ctx)
	return err == nil && found
}

func (h *DatabaseTransferHandlers) requireOwner(c *gin.Context) {
	u, _ := contexts.GetUser(c.Request.Context())
	if u == nil || !u.IsOwner || !h.isOwner(c.Request.Context(), u.ID) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "owner_required"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Next()
}

func transferError(c *gin.Context, err error) {
	code := datatransfer.ErrorCode(err)
	status := http.StatusBadRequest
	if code == "busy" || code == "submission_conflict" {
		status = http.StatusConflict
	}
	if code == "operation_failed" || code == "database_tool_failed" {
		status = http.StatusInternalServerError
	}
	c.JSON(status, gin.H{"error": code})
}

func bindTransfer(c *gin.Context, target any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128*1024)
	if err := c.ShouldBindJSON(target); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_package"})
		return false
	}
	return true
}

func (h *DatabaseTransferHandlers) RegisterRoutes(group *gin.RouterGroup) {
	group.Use(h.requireOwner)
	group.GET("/status", func(c *gin.Context) {
		value, err := h.service.Status(c.Request.Context())
		if err != nil {
			transferError(c, err)
			return
		}
		c.JSON(http.StatusOK, value)
	})
	group.GET("/job", func(c *gin.Context) {
		value, err := h.service.Job(c.Request.Context())
		if err != nil {
			transferError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"job": value})
	})
	group.POST("/export", func(c *gin.Context) {
		var input struct {
			ID string `json:"id" binding:"required"`
		}
		if !bindTransfer(c, &input) {
			return
		}
		job, err := h.service.StartExport(c.Request.Context(), input.ID)
		if err != nil {
			transferError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, job)
	})
	group.POST("/uploads", func(c *gin.Context) {
		var input struct {
			ID       string `json:"id" binding:"required"`
			Bytes    int64  `json:"bytes"`
			Manifest string `json:"manifest"`
		}
		if !bindTransfer(c, &input) {
			return
		}
		if len(input.Manifest) == 0 || len(input.Manifest) > 64*1024 {
			transferError(c, datatransfer.ErrPackage)
			return
		}
		digest := sha256.Sum256([]byte(input.Manifest))
		job, err := h.service.InitializeUpload(c.Request.Context(), input.ID, input.Bytes, hex.EncodeToString(digest[:]))
		if err != nil {
			transferError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, job)
	})
	group.PUT("/uploads/:id", func(c *gin.Context) {
		deadline := time.Now().Add(h.service.Timeout())
		controller := http.NewResponseController(c.Writer)
		_ = controller.SetReadDeadline(deadline)
		_ = controller.SetWriteDeadline(deadline)
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.service.MaxUploadBytes())
		body := &transferUploadBody{ReadCloser: c.Request.Body, controller: controller}
		job, err := h.service.Upload(c.Request.Context(), c.Param("id"), body)
		if err != nil {
			transferError(c, err)
			return
		}
		c.JSON(http.StatusOK, job)
	})
	group.POST("/imports", func(c *gin.Context) {
		var input struct {
			ID             string `json:"id"`
			TargetDatabase string `json:"targetDatabase"`
			Confirmed      bool   `json:"confirmed"`
		}
		if !bindTransfer(c, &input) {
			return
		}
		if !input.Confirmed {
			c.JSON(http.StatusBadRequest, gin.H{"error": "confirmation_required"})
			return
		}
		job, err := h.service.StartImport(c.Request.Context(), input.ID, input.TargetDatabase)
		if err != nil {
			transferError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, job)
	})
	group.DELETE("/jobs/:id", func(c *gin.Context) {
		if err := h.service.Discard(c.Request.Context(), c.Param("id")); err != nil {
			transferError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
	group.POST("/jobs/:id/download-ticket", h.downloadTicket)
}

// Close must interrupt a blocked network Read before the standard request body
// can take its close mutex. Used when the worker loses ownership or disk space.
type transferUploadBody struct {
	io.ReadCloser
	controller *http.ResponseController
}

type transferDownloadWriter struct {
	io.Writer
	controller *http.ResponseController
}

func (writer *transferDownloadWriter) Close() error {
	return writer.controller.SetWriteDeadline(time.Now())
}

func (body *transferUploadBody) Close() error {
	_ = body.controller.SetReadDeadline(time.Now())
	return body.ReadCloser.Close()
}

type migrationTicket struct {
	Job     string `json:"job"`
	Digest  string `json:"digest"`
	User    int    `json:"user"`
	Expires int64  `json:"expires"`
	Purpose string `json:"purpose"`
}

const migrationCookie = "axonhub_migration_download"
const migrationFilePath = "/admin/database-transfer/files/"

func signMigrationTicket(ticket migrationTicket, secret string) (string, error) {
	data, err := json.Marshal(ticket)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func verifyMigrationTicket(raw, secret string) (migrationTicket, bool) {
	var ticket migrationTicket
	if len(raw) > 2048 {
		return ticket, false
	}
	payload, sig, ok := strings.Cut(raw, ".")
	if !ok {
		return ticket, false
	}
	signature, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return ticket, false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return ticket, false
	}
	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || json.Unmarshal(data, &ticket) != nil {
		return ticket, false
	}
	now := time.Now().Unix()
	return ticket, ticket.Purpose == "database-transfer-download" && ticket.User > 0 && ticket.Expires > now && ticket.Expires <= now+300
}

func (h *DatabaseTransferHandlers) downloadTicket(c *gin.Context) {
	job, err := h.service.Job(c.Request.Context())
	if err != nil {
		transferError(c, err)
		return
	}
	if job == nil || job.ID != c.Param("id") || !job.DownloadAvailable {
		transferError(c, datatransfer.ErrMissing)
		return
	}
	secret, err := h.system.SecretKey(c.Request.Context())
	if err != nil {
		transferError(c, err)
		return
	}
	owner, _ := contexts.GetUser(c.Request.Context())
	ticket, err := signMigrationTicket(migrationTicket{Job: job.ID, Digest: job.Manifest.SHA256, User: owner.ID, Expires: time.Now().Add(5 * time.Minute).Unix(), Purpose: "database-transfer-download"}, secret)
	if err != nil {
		transferError(c, err)
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{Name: migrationCookie, Value: ticket, Path: migrationFilePath + job.ID, MaxAge: 300, HttpOnly: true, Secure: c.Request.TLS != nil || strings.HasPrefix(h.publicURL, "https://"), SameSite: http.SameSiteStrictMode})
	c.JSON(http.StatusOK, gin.H{"url": migrationFilePath + job.ID})
}

func (h *DatabaseTransferHandlers) Download(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	ctx := authz.WithSystemBypass(c.Request.Context(), "migration-download-ticket")
	raw, err := c.Cookie(migrationCookie)
	if err != nil {
		c.Status(http.StatusUnauthorized)
		return
	}
	secret, err := h.system.SecretKey(ctx)
	if err != nil {
		c.Status(http.StatusUnauthorized)
		return
	}
	ticket, valid := verifyMigrationTicket(raw, secret)
	if !valid || ticket.Job != c.Param("id") || !h.isOwner(ctx, ticket.User) {
		c.Status(http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, h.service.Timeout())
	defer cancel()
	_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(h.service.Timeout()))
	err = h.service.StreamDownload(ctx, ticket.Job, ticket.Digest, func(size int64) io.Writer {
		c.Header("Content-Type", "application/x-tar")
		c.Header("Content-Disposition", `attachment; filename="axonhub-`+ticket.Job+`.axon.tar"`)
		c.Header("Content-Length", strconv.FormatInt(size, 10))
		c.Header("Accept-Ranges", "none")
		if c.Request.Method == http.MethodHead {
			return nil
		}
		return &transferDownloadWriter{Writer: c.Writer, controller: http.NewResponseController(c.Writer)}
	})
	if err != nil && !c.Writer.Written() {
		transferError(c, err)
	}
}
