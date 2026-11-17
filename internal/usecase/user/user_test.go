package user_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/mocks"
	"github.com/xingyunqi-tech/go-template/internal/usecase/user"
	usermocks "github.com/xingyunqi-tech/go-template/internal/usecase/user/mocks"
)

var errStore = errors.New("store down")

type deps struct {
	users    *mocks.MockUsers
	files    *mocks.MockFiles
	sessions *usermocks.MockSessions
	hasher   *usermocks.MockHasher
	cleanup  *usermocks.MockAccountCleanup
}

func newService(t *testing.T) (*user.Service, deps) {
	t.Helper()

	ctrl := gomock.NewController(t)
	d := deps{
		users: mocks.NewMockUsers(ctrl), files: mocks.NewMockFiles(ctrl), sessions: usermocks.NewMockSessions(ctrl),
		hasher: usermocks.NewMockHasher(ctrl), cleanup: usermocks.NewMockAccountCleanup(ctrl),
	}

	return user.New(d.users, d.files, d.sessions, d.hasher, d.cleanup, slog.New(slog.DiscardHandler)), d
}

func ptr[T any](v T) *T { return &v }

func requireInvalid(t *testing.T, err error, field, key string) {
	t.Helper()

	var e *entity.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, entity.StatusUnprocessable, e.Status)
	assert.Equal(t, []entity.Detail{{Name: field, Value: key}}, e.Fields)
}

func requireForbidden(t *testing.T, err error) {
	t.Helper()

	var e *entity.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, entity.StatusForbidden, e.Status)
	assert.Equal(t, "fileNotAccessible", e.ErrorKey)
}

func TestCreateHashesPasswordAndResolvesRelations(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	photo := entity.File{ID: uuid.New(), Visibility: entity.VisibilityPublic}
	created := &entity.User{ID: 9}

	d.hasher.EXPECT().Hash(gomock.Any(), "secret").Return("hashed", nil)
	d.users.EXPECT().Get(gomock.Any(), repo.UserQuery{Email: ptr("a@example.com")}).Return(nil, nil)
	d.files.EXPECT().GetMany(gomock.Any(), []uuid.UUID{photo.ID}).Return([]entity.File{photo}, nil)
	d.users.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, u *repo.NewUser) (int, error) {
		assert.Equal(t, ptr("hashed"), u.Password)
		assert.Equal(t, ptr("a@example.com"), u.Email)
		assert.Equal(t, entity.ProviderEmail, u.Provider)
		assert.Equal(t, ptr(0), u.Gender)
		assert.Equal(t, &photo.ID, u.PhotoID)
		assert.Equal(t, ptr(entity.RoleAdmin), u.RoleID)
		assert.Nil(t, u.StatusID)

		return 9, nil
	})
	d.users.EXPECT().Get(gomock.Any(), repo.UserQuery{ID: ptr(9)}).Return(created, nil)

	u, rel, err := svc.Create(context.Background(), &user.Input{
		Email: repo.Some("a@example.com"), Password: repo.Some("secret"),
		Photo: user.Ref{Set: true, ID: photo.ID.String()}, Role: user.Ref{Set: true, ID: "1"}, Status: user.Ref{Set: true, Null: true},
	})
	require.NoError(t, err)
	assert.Same(t, created, u)
	assert.Equal(t, photo.ID, rel.Photo.ID)
	assert.Equal(t, ptr(entity.RoleAdmin), rel.RoleID)
}

func TestCreateRejectsTakenEmailAndPhone(t *testing.T) {
	t.Parallel()

	t.Run("email", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.users.EXPECT().Get(gomock.Any(), repo.UserQuery{Email: ptr("a@example.com")}).Return(&entity.User{ID: 2}, nil)

		_, _, err := svc.Create(context.Background(), &user.Input{Email: repo.Some("a@example.com")})
		requireInvalid(t, err, "email", "emailAlreadyExists")
	})

	t.Run("phone", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.users.EXPECT().Get(gomock.Any(), repo.UserQuery{Phone: ptr("13800000000")}).Return(&entity.User{ID: 2}, nil)

		_, _, err := svc.Create(context.Background(), &user.Input{Phone: repo.Some("13800000000")})
		requireInvalid(t, err, "phone", "phoneAlreadyExists")
	})
}

func TestCreateRejectsInvalidRelations(t *testing.T) {
	t.Parallel()

	t.Run("unknown role", func(t *testing.T) {
		t.Parallel()

		svc, _ := newService(t)
		_, _, err := svc.Create(context.Background(), &user.Input{Role: user.Ref{Set: true, ID: "99"}})
		requireInvalid(t, err, "role", "roleNotExists")
	})

	t.Run("unknown status", func(t *testing.T) {
		t.Parallel()

		svc, _ := newService(t)
		_, _, err := svc.Create(context.Background(), &user.Input{Status: user.Ref{Set: true, ID: "99"}})
		requireInvalid(t, err, "status", "statusNotExists")
	})

	t.Run("missing photo", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.files.EXPECT().GetMany(gomock.Any(), gomock.Any()).Return(nil, nil)

		_, _, err := svc.Create(context.Background(), &user.Input{Photo: user.Ref{Set: true, ID: uuid.New().String()}})
		requireInvalid(t, err, "photo", "imageNotExists")
	})

	t.Run("private photo", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.files.EXPECT().GetMany(gomock.Any(), gomock.Any()).Return([]entity.File{{Visibility: entity.VisibilityPrivate, OwnerUserID: ptr(1)}}, nil)

		_, _, err := svc.Create(context.Background(), &user.Input{Photo: user.Ref{Set: true, ID: uuid.New().String()}})
		requireForbidden(t, err)
	})

	t.Run("malformed photo id", func(t *testing.T) {
		t.Parallel()

		svc, _ := newService(t)
		_, _, err := svc.Create(context.Background(), &user.Input{Photo: user.Ref{Set: true, ID: "nope"}})
		require.Error(t, err)
	})
}

func TestCreateReturnsDependencyErrors(t *testing.T) {
	t.Parallel()

	t.Run("hasher", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.hasher.EXPECT().Hash(gomock.Any(), "secret").Return("", errStore)

		_, _, err := svc.Create(context.Background(), &user.Input{Password: repo.Some("secret")})
		require.ErrorIs(t, err, errStore)
	})

	t.Run("insert", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.users.EXPECT().Create(gomock.Any(), gomock.Any()).Return(0, errStore)

		_, _, err := svc.Create(context.Background(), &user.Input{})
		require.ErrorIs(t, err, errStore)
	})
}

func TestFindByEmailSkipsEmptyEmail(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	found := &entity.User{ID: 2}
	d.users.EXPECT().Get(gomock.Any(), repo.UserQuery{Email: ptr("a@example.com")}).Return(found, nil)

	u, err := svc.FindByEmail(context.Background(), "")
	require.NoError(t, err)
	assert.Nil(t, u)

	u, err = svc.FindByEmail(context.Background(), "a@example.com")
	require.NoError(t, err)
	assert.Same(t, found, u)
}

func TestUpdateAllowsOwnEmailAndClearsPhoneAndPhoto(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	updated := &entity.User{ID: 5}

	d.users.EXPECT().Get(gomock.Any(), repo.UserQuery{Email: ptr("a@example.com")}).Return(&entity.User{ID: 5}, nil)
	d.users.EXPECT().Update(gomock.Any(), 5, gomock.Any()).DoAndReturn(func(_ context.Context, _ int, c *repo.UserChanges) (bool, error) {
		assert.Equal(t, repo.Some("a@example.com"), c.Email)
		assert.Equal(t, repo.Null[string](), c.Phone)
		assert.Equal(t, repo.Null[uuid.UUID](), c.PhotoID)
		assert.False(t, c.Password.Set)
		assert.Equal(t, ptr(entity.StatusActive), c.StatusID)

		return true, nil
	})
	d.users.EXPECT().Get(gomock.Any(), repo.UserQuery{ID: ptr(5)}).Return(updated, nil)

	u, err := svc.Update(context.Background(), 5, &user.Input{
		Email: repo.Some("a@example.com"), Phone: repo.Null[string](), Photo: user.Ref{Set: true, Null: true},
		Status: user.Ref{Set: true, ID: "1"},
	})
	require.NoError(t, err)
	assert.Same(t, updated, u)
}

func TestUpdateAcceptsOwnPrivatePhoto(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	photo := entity.File{ID: uuid.New(), Visibility: entity.VisibilityPrivate, OwnerUserID: ptr(5)}

	d.files.EXPECT().GetMany(gomock.Any(), []uuid.UUID{photo.ID}).Return([]entity.File{photo}, nil)
	d.users.EXPECT().Update(gomock.Any(), 5, gomock.Any()).DoAndReturn(func(_ context.Context, _ int, c *repo.UserChanges) (bool, error) {
		assert.Equal(t, repo.Some(photo.ID), c.PhotoID)

		return true, nil
	})
	d.users.EXPECT().Get(gomock.Any(), repo.UserQuery{ID: ptr(5)}).Return(&entity.User{ID: 5}, nil)

	_, err := svc.Update(context.Background(), 5, &user.Input{Photo: user.Ref{Set: true, ID: photo.ID.String()}})
	require.NoError(t, err)
}

func TestUpdateRejectsPhoneOfAnotherUser(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	d.users.EXPECT().Get(gomock.Any(), repo.UserQuery{Phone: ptr("13800000000")}).Return(&entity.User{ID: 6}, nil)

	_, err := svc.Update(context.Background(), 5, &user.Input{Phone: repo.Some("13800000000")})
	requireInvalid(t, err, "phone", "phoneAlreadyExists")
}

func TestUpdateOfMissingUserIsNotFound(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	d.users.EXPECT().Update(gomock.Any(), 5, gomock.Any()).Return(false, nil)
	d.users.EXPECT().Get(gomock.Any(), repo.UserQuery{ID: ptr(5)}).Return(nil, nil)

	_, err := svc.Update(context.Background(), 5, &user.Input{})

	var e *entity.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, entity.StatusNotFound, e.Status)
	assert.Equal(t, "User not found", e.Message)
}

func TestUpdateByAdminSignsOutOnlyWhenPasswordChanges(t *testing.T) {
	t.Parallel()

	t.Run("password", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.hasher.EXPECT().Hash(gomock.Any(), "secret").Return("hashed", nil)
		d.users.EXPECT().Update(gomock.Any(), 5, gomock.Any()).DoAndReturn(func(_ context.Context, _ int, c *repo.UserChanges) (bool, error) {
			assert.Equal(t, repo.Some("hashed"), c.Password)

			return true, nil
		})
		d.users.EXPECT().Get(gomock.Any(), repo.UserQuery{ID: ptr(5)}).Return(&entity.User{ID: 5}, nil)
		d.sessions.EXPECT().DeleteAllForUser(gomock.Any(), 5, nil).Return(nil)

		_, err := svc.UpdateByAdmin(context.Background(), 5, &user.Input{Password: repo.Some("secret")})
		require.NoError(t, err)
	})

	t.Run("profile only", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.users.EXPECT().Update(gomock.Any(), 5, gomock.Any()).Return(true, nil)
		d.users.EXPECT().Get(gomock.Any(), repo.UserQuery{ID: ptr(5)}).Return(&entity.User{ID: 5}, nil)

		_, err := svc.UpdateByAdmin(context.Background(), 5, &user.Input{Nickname: repo.Some("n")})
		require.NoError(t, err)
	})
}

func TestRemoveSignsOutDeletesAndSchedulesCleanup(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	gomock.InOrder(
		d.sessions.EXPECT().DeleteAllForUser(gomock.Any(), 5, nil).Return(nil),
		d.users.EXPECT().Remove(gomock.Any(), 5).Return(nil),
		d.cleanup.EXPECT().Enqueue(gomock.Any(), 5).Return(nil),
	)

	require.NoError(t, svc.Remove(context.Background(), 5))
}

func TestRemoveStopsAtFirstError(t *testing.T) {
	t.Parallel()

	t.Run("sessions", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.sessions.EXPECT().DeleteAllForUser(gomock.Any(), 5, nil).Return(errStore)
		require.ErrorIs(t, svc.Remove(context.Background(), 5), errStore)
	})

	t.Run("users", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.sessions.EXPECT().DeleteAllForUser(gomock.Any(), 5, nil).Return(nil)
		d.users.EXPECT().Remove(gomock.Any(), 5).Return(errStore)
		require.ErrorIs(t, svc.Remove(context.Background(), 5), errStore)
	})
}
