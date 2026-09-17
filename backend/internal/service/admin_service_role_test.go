//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

func TestAdminService_CreateUser_WithAdminRoleRequiresSuperAdminActor(t *testing.T) {
	repo := &userRepoStub{nextID: 30, usersByID: map[int64]*User{
		1: &User{ID: 1, Role: RoleSuperAdmin, Status: StatusActive},
	}}
	svc := &adminServiceImpl{userRepo: repo}

	user, err := svc.CreateUser(context.Background(), &CreateUserInput{
		Email:        "admin@test.com",
		Password:     "strong-pass",
		Role:         RoleAdmin,
		ActorAdminID: 1,
	})
	require.NoError(t, err)
	require.Equal(t, RoleAdmin, user.Role)
}

func TestAdminService_CreateUser_AdminRoleRejectedForPlainAdminActor(t *testing.T) {
	repo := &userRepoStub{nextID: 30, usersByID: map[int64]*User{
		2: &User{ID: 2, Role: RoleAdmin, Status: StatusActive},
	}}
	svc := &adminServiceImpl{userRepo: repo}

	_, err := svc.CreateUser(context.Background(), &CreateUserInput{
		Email:        "admin@test.com",
		Password:     "strong-pass",
		Role:         RoleAdmin,
		ActorAdminID: 2,
	})
	require.ErrorIs(t, err, ErrInsufficientPerms)
	require.Empty(t, repo.created)
}

func TestAdminService_CreateUser_DefaultsToUserRole(t *testing.T) {
	repo := &userRepoStub{nextID: 31}
	svc := &adminServiceImpl{userRepo: repo}

	user, err := svc.CreateUser(context.Background(), &CreateUserInput{
		Email:    "plain@test.com",
		Password: "strong-pass",
	})
	require.NoError(t, err)
	require.Equal(t, RoleUser, user.Role)
}

func TestAdminService_CreateUser_InvalidRoleRejected(t *testing.T) {
	repo := &userRepoStub{nextID: 32}
	svc := &adminServiceImpl{userRepo: repo}

	_, err := svc.CreateUser(context.Background(), &CreateUserInput{
		Email:    "bad@test.com",
		Password: "strong-pass",
		Role:     "superuser",
	})
	require.Error(t, err)
	require.Empty(t, repo.created, "非法角色不应写入用户")
}

func TestAdminService_UpdateUser_PromoteToAdmin(t *testing.T) {
	target := &User{ID: 42, Email: "u@example.com", Role: RoleUser, Status: StatusActive}
	actor := &User{ID: 1, Email: "root@example.com", Role: RoleSuperAdmin, Status: StatusActive}
	base := &userRepoStub{user: target, usersByID: map[int64]*User{42: target, 1: actor}}
	repo := &rpmUserRepoStub{userRepoStub: base}
	invalidator := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{
		userRepo:             repo,
		redeemCodeRepo:       &redeemRepoStub{},
		authCacheInvalidator: invalidator,
	}

	updated, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{Role: RoleAdmin, ActorAdminID: 1})
	require.NoError(t, err)
	require.Equal(t, RoleAdmin, updated.Role)
	require.Equal(t, []int64{42}, invalidator.userIDs, "角色变更应失效认证缓存")
}

func TestAdminService_UpdateUser_AdminEditRejectedForPlainAdminActor(t *testing.T) {
	target := &User{ID: 42, Email: "a@example.com", Role: RoleAdmin, Status: StatusActive}
	actor := &User{ID: 2, Email: "admin@example.com", Role: RoleAdmin, Status: StatusActive}
	base := &userRepoStub{user: target, usersByID: map[int64]*User{42: target, 2: actor}}
	repo := &rpmUserRepoStub{userRepoStub: base}
	svc := &adminServiceImpl{userRepo: repo, redeemCodeRepo: &redeemRepoStub{}}

	newName := "renamed"
	_, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{Username: &newName, ActorAdminID: 2})
	require.ErrorIs(t, err, ErrInsufficientPerms)
	require.Nil(t, repo.lastUpdated)
}

func TestAdminService_UpdateUser_RoleOmittedKeepsExisting(t *testing.T) {
	target := &User{ID: 42, Email: "u@example.com", Role: RoleAdmin, Status: StatusActive}
	actor := &User{ID: 1, Email: "root@example.com", Role: RoleSuperAdmin, Status: StatusActive}
	base := &userRepoStub{user: target, usersByID: map[int64]*User{42: target, 1: actor}}
	repo := &rpmUserRepoStub{userRepoStub: base}
	svc := &adminServiceImpl{userRepo: repo, redeemCodeRepo: &redeemRepoStub{}}

	newName := "renamed"
	updated, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{Username: &newName, ActorAdminID: 1})
	require.NoError(t, err)
	require.Equal(t, RoleAdmin, updated.Role, "未提供 role 时不应改变现有角色")
}

func TestAdminService_UpdateUser_InvalidRoleRejected(t *testing.T) {
	base := &userRepoStub{user: &User{ID: 42, Email: "u@example.com", Role: RoleUser}}
	repo := &rpmUserRepoStub{userRepoStub: base}
	svc := &adminServiceImpl{userRepo: repo, redeemCodeRepo: &redeemRepoStub{}}

	_, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{Role: "root"})
	require.Error(t, err)
	require.Nil(t, repo.lastUpdated, "非法角色不应触发持久化")
}

type roleGuardUserRepoStub struct {
	*rpmUserRepoStub
	adminTotal      int64
	superAdminTotal int64
	listCalls       int
	statusFilters   []string
}

func (s *roleGuardUserRepoStub) ListWithFilters(_ context.Context, _ pagination.PaginationParams, filters UserListFilters) ([]User, *pagination.PaginationResult, error) {
	s.listCalls++
	s.statusFilters = append(s.statusFilters, filters.Status)
	switch filters.Role {
	case RoleSuperAdmin:
		return nil, &pagination.PaginationResult{Total: s.superAdminTotal}, nil
	case RoleAdmin:
		return nil, &pagination.PaginationResult{Total: s.adminTotal}, nil
	default:
		return nil, &pagination.PaginationResult{Total: 0}, nil
	}
}

func TestAdminService_UpdateUser_DemoteLastAdminRejected(t *testing.T) {
	target := &User{ID: 42, Email: "a@example.com", Role: RoleAdmin, Status: StatusActive}
	actor := &User{ID: 1, Role: RoleSuperAdmin, Status: StatusActive}
	base := &userRepoStub{user: target, usersByID: map[int64]*User{42: target, 1: actor}}
	repo := &roleGuardUserRepoStub{rpmUserRepoStub: &rpmUserRepoStub{userRepoStub: base}, adminTotal: 1, superAdminTotal: 0}
	svc := &adminServiceImpl{userRepo: repo, redeemCodeRepo: &redeemRepoStub{}}

	_, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{Role: RoleUser, ActorAdminID: 1})
	require.Error(t, err)
	require.Contains(t, err.Error(), "last admin")
	require.Nil(t, repo.lastUpdated, "最后一个管理员不应被降级持久化")
	require.Equal(t, 2, repo.listCalls, "降级路径应统计 admin 与 super_admin")
	require.Equal(t, []string{StatusActive, StatusActive}, repo.statusFilters)
}

func TestAdminService_UpdateUser_DemoteLastSuperAdminRejected(t *testing.T) {
	target := &User{ID: 1, Email: "root@example.com", Role: RoleSuperAdmin, Status: StatusActive}
	base := &userRepoStub{user: target, usersByID: map[int64]*User{1: target}}
	repo := &roleGuardUserRepoStub{rpmUserRepoStub: &rpmUserRepoStub{userRepoStub: base}, adminTotal: 1, superAdminTotal: 1}
	svc := &adminServiceImpl{userRepo: repo, redeemCodeRepo: &redeemRepoStub{}}

	_, err := svc.UpdateUser(context.Background(), 1, &UpdateUserInput{Role: RoleAdmin, ActorAdminID: 1})
	require.Error(t, err)
	require.Contains(t, err.Error(), "last super admin")
	require.Nil(t, repo.lastUpdated)
	require.Equal(t, []string{StatusActive}, repo.statusFilters)
}

func TestAdminService_UpdateUser_DemoteAdminAllowedWhenOthersExist(t *testing.T) {
	target := &User{ID: 42, Email: "a@example.com", Role: RoleAdmin, Status: StatusActive}
	actor := &User{ID: 1, Role: RoleSuperAdmin, Status: StatusActive}
	base := &userRepoStub{user: target, usersByID: map[int64]*User{42: target, 1: actor}}
	repo := &roleGuardUserRepoStub{rpmUserRepoStub: &rpmUserRepoStub{userRepoStub: base}, adminTotal: 2, superAdminTotal: 1}
	invalidator := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{
		userRepo:             repo,
		redeemCodeRepo:       &redeemRepoStub{},
		authCacheInvalidator: invalidator,
	}

	updated, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{Role: RoleUser, ActorAdminID: 1})
	require.NoError(t, err)
	require.Equal(t, RoleUser, updated.Role)
	require.NotNil(t, repo.lastUpdated)
	require.Equal(t, RoleUser, repo.lastUpdated.Role, "存在其他管理员时允许降级")
}

func TestAdminService_UpdateUser_PromoteDoesNotCountAdmins(t *testing.T) {
	target := &User{ID: 42, Email: "u@example.com", Role: RoleUser, Status: StatusActive}
	actor := &User{ID: 1, Role: RoleSuperAdmin, Status: StatusActive}
	base := &userRepoStub{user: target, usersByID: map[int64]*User{42: target, 1: actor}}
	repo := &roleGuardUserRepoStub{rpmUserRepoStub: &rpmUserRepoStub{userRepoStub: base}, adminTotal: 1}
	svc := &adminServiceImpl{
		userRepo:             repo,
		redeemCodeRepo:       &redeemRepoStub{},
		authCacheInvalidator: &authCacheInvalidatorStub{},
	}

	updated, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{Role: RoleAdmin, ActorAdminID: 1})
	require.NoError(t, err)
	require.Equal(t, RoleAdmin, updated.Role)
	require.Equal(t, 0, repo.listCalls, "升级路径不应触发管理员计数")
}

func TestAdminService_AuthorizeUserMutation_PlainAdminCannotTargetPrivilegedUser(t *testing.T) {
	actor := &User{ID: 2, Role: RoleAdmin, Status: StatusActive}
	target := &User{ID: 42, Role: RoleSuperAdmin, Status: StatusActive}
	repo := &userRepoStub{usersByID: map[int64]*User{
		actor.ID:  actor,
		target.ID: target,
	}}
	svc := &adminServiceImpl{userRepo: repo}

	err := svc.AuthorizeUserMutation(context.Background(), actor.ID, target.ID)

	require.ErrorIs(t, err, ErrInsufficientPerms)
}

func TestAdminService_AuthorizeUserMutation_SuperAdminCanTargetPrivilegedUser(t *testing.T) {
	actor := &User{ID: 1, Role: RoleSuperAdmin, Status: StatusActive}
	target := &User{ID: 42, Role: RoleAdmin, Status: StatusActive}
	repo := &userRepoStub{usersByID: map[int64]*User{
		actor.ID:  actor,
		target.ID: target,
	}}
	svc := &adminServiceImpl{userRepo: repo}

	err := svc.AuthorizeUserMutation(context.Background(), actor.ID, target.ID)

	require.NoError(t, err)
}

func TestAdminService_AuthorizeUserMutation_PlainAdminCanTargetNormalUsers(t *testing.T) {
	targetA := &User{ID: 41, Role: RoleUser, Status: StatusActive}
	targetB := &User{ID: 42, Role: RoleUser, Status: StatusDisabled}
	repo := &userRepoStub{usersByID: map[int64]*User{
		targetA.ID: targetA,
		targetB.ID: targetB,
	}}
	svc := &adminServiceImpl{userRepo: repo}

	err := svc.AuthorizeUserMutation(context.Background(), 2, targetA.ID, targetB.ID)

	require.NoError(t, err)
}

func TestAdminService_AuthorizeUserMutation_DisabledSuperAdminActorRejected(t *testing.T) {
	actor := &User{ID: 1, Role: RoleSuperAdmin, Status: StatusDisabled}
	target := &User{ID: 42, Role: RoleAdmin, Status: StatusActive}
	repo := &userRepoStub{usersByID: map[int64]*User{
		actor.ID:  actor,
		target.ID: target,
	}}
	svc := &adminServiceImpl{userRepo: repo}

	err := svc.AuthorizeUserMutation(context.Background(), actor.ID, target.ID)

	require.ErrorIs(t, err, ErrInsufficientPerms)
}
