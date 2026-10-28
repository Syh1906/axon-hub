package biz

import (
	"context"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/user"
)

func TestCreateUserGrantPermissions(t *testing.T) {
	for _, tc := range []struct {
		name       string
		actorOwner bool
		scope      string
		roleScope  string
		wantError  bool
	}{
		{name: "不能直接授予自己没有的权限", scope: "write_channels", wantError: true},
		{name: "不能通过角色授予自己没有的权限", roleScope: "write_channels", wantError: true},
		{name: "可以授予自己持有的直接权限", scope: "read_users"},
		{name: "可以授予自己持有的角色权限", roleScope: "read_users"},
		{name: "所有者可以授予直接权限和角色", actorOwner: true, scope: "write_channels", roleScope: "write_roles"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, client := setupTestUserService(t)
			defer client.Close()
			seedCtx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
			actor := client.User.Create().SetEmail("create-admin@example.com").SetPassword("password").
				SetIsOwner(tc.actorOwner).SetScopes([]string{"read_users", "write_users"}).SaveX(seedCtx)
			input := ent.CreateUserInput{Email: "new-user@example.com", Password: "test-password"}
			if tc.scope != "" {
				input.Scopes = []string{tc.scope}
			}
			if tc.roleScope != "" {
				grantRole := client.Role.Create().SetName("待授予角色").SetScopes([]string{tc.roleScope}).SaveX(seedCtx)
				input.RoleIDs = []int{grantRole.ID}
			}

			ctx := contexts.WithUser(ent.NewContext(context.Background(), client), actor)
			created, err := svc.CreateUser(ctx, input)
			if tc.wantError {
				require.ErrorContains(t, err, "permission denied")
				require.Nil(t, created)
				require.False(t, client.User.Query().Where(user.EmailEQ(input.Email)).ExistX(seedCtx), "拒绝授权不能留下新账户")
				return
			}
			require.NoError(t, err)
			require.ElementsMatch(t, input.Scopes, created.Scopes)
			roles, err := created.QueryRoles().IDs(seedCtx)
			require.NoError(t, err)
			require.ElementsMatch(t, input.RoleIDs, roles)
		})
	}
}

func TestUpdateUserPermissionEscalation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		actorOwner bool
		self       bool
		input      ent.UpdateUserInput
		wantError  bool
	}{
		{name: "不能将自己设为系统所有者", self: true, input: ent.UpdateUserInput{IsOwner: lo.ToPtr(true)}, wantError: true},
		{name: "不能将其他账户设为系统所有者", input: ent.UpdateUserInput{IsOwner: lo.ToPtr(true)}, wantError: true},
		{name: "追加权限也不能超出自己持有的权限", self: true, input: ent.UpdateUserInput{AppendScopes: []string{"write_channels"}}, wantError: true},
		{name: "普通账户表单可保留非所有者状态", input: ent.UpdateUserInput{IsOwner: lo.ToPtr(false), FirstName: lo.ToPtr("修改后")}},
		{name: "可以追加自己持有的权限", input: ent.UpdateUserInput{AppendScopes: []string{"read_users"}}},
		{name: "所有者可以设置其他系统所有者", actorOwner: true, input: ent.UpdateUserInput{IsOwner: lo.ToPtr(true)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, client := setupTestUserService(t)
			defer client.Close()
			seedCtx := authz.WithTestBypass(ent.NewContext(context.Background(), client))
			actor := client.User.Create().SetEmail("edit-admin@example.com").SetPassword("password").
				SetIsOwner(tc.actorOwner).SetScopes([]string{"read_users", "write_users"}).SaveX(seedCtx)
			target := actor
			if !tc.self {
				target = client.User.Create().SetEmail("edit-target@example.com").SetPassword("password").SaveX(seedCtx)
			}
			ctx := contexts.WithUser(ent.NewContext(context.Background(), client), actor)
			updated, err := svc.UpdateUser(ctx, target.ID, tc.input)
			if tc.wantError {
				require.ErrorContains(t, err, "permission denied")
				require.Nil(t, updated)
				unchanged := client.User.GetX(seedCtx, target.ID)
				require.Equal(t, target.IsOwner, unchanged.IsOwner)
				require.ElementsMatch(t, target.Scopes, unchanged.Scopes)
				return
			}
			require.NoError(t, err)
			if tc.input.IsOwner != nil {
				require.Equal(t, *tc.input.IsOwner, updated.IsOwner)
			}
			if tc.input.FirstName != nil {
				require.Equal(t, *tc.input.FirstName, updated.FirstName)
			}
			for _, scope := range tc.input.AppendScopes {
				require.Contains(t, updated.Scopes, scope)
			}
		})
	}
}
