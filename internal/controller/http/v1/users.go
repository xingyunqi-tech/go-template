package v1

import (
	"context"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/response"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
	"github.com/xingyunqi-tech/go-template/pkg/pagination"
)

// ListUsers handles GET /api/v1/users.
func (s *Server) ListUsers(ctx context.Context, req gen.ListUsersRequestObject) (gen.ListUsersResponseObject, error) {
	q := &usecase.UserQuery{
		Keyword: req.Params.Keyword, Status: (*string)(req.Params.Status), Role: req.Params.Role,
		Sort: "", Page: 1, PageSize: pagination.DefaultSize,
	}

	if req.Params.Sort != nil {
		q.Sort = *req.Params.Sort
	}

	if req.Params.Page != nil {
		q.Page = *req.Params.Page
	}

	if req.Params.PageSize != nil {
		q.PageSize = *req.Params.PageSize
	}

	page, err := s.users.List(ctx, q)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	items := make([]gen.User, 0, len(page.Items))
	for i := range page.Items {
		items = append(items, userOf(&page.Items[i]))
	}

	return gen.ListUsers200JSONResponse{Code: response.Code, Message: response.Message, Data: gen.UserPage{
		Items: items, Total: int(page.Total), Page: page.Page, PageSize: page.PageSize,
	}}, nil
}

// CreateUser handles POST /api/v1/users.
func (s *Server) CreateUser(ctx context.Context, req gen.CreateUserRequestObject) (gen.CreateUserResponseObject, error) {
	u, err := s.users.Create(ctx, &usecase.NewAccount{
		Email: (*string)(req.Body.Email), Phone: req.Body.Phone, Password: req.Body.Password, DisplayName: req.Body.DisplayName,
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	return gen.CreateUser201JSONResponse(userEnvelope(u)), nil
}

// GetUser handles GET /api/v1/users/{id}.
func (s *Server) GetUser(ctx context.Context, req gen.GetUserRequestObject) (gen.GetUserResponseObject, error) {
	u, err := s.users.Get(ctx, req.Id)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	return gen.GetUser200JSONResponse(userEnvelope(u)), nil
}

// UpdateUser handles PATCH /api/v1/users/{id}.
func (s *Server) UpdateUser(ctx context.Context, req gen.UpdateUserRequestObject) (gen.UpdateUserResponseObject, error) {
	u, err := s.users.Update(ctx, req.Id, req.Body.DisplayName, (*string)(req.Body.Status))
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	return gen.UpdateUser200JSONResponse(userEnvelope(u)), nil
}

// SetUserRoles handles PUT /api/v1/users/{id}/roles.
func (s *Server) SetUserRoles(ctx context.Context, req gen.SetUserRolesRequestObject) (gen.SetUserRolesResponseObject, error) {
	u, err := s.users.SetRoles(ctx, req.Id, req.Body.Roles)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	return gen.SetUserRoles200JSONResponse(userEnvelope(u)), nil
}

// ListRoles handles GET /api/v1/roles.
func (s *Server) ListRoles(context.Context, gen.ListRolesRequestObject) (gen.ListRolesResponseObject, error) {
	roles := entity.Roles()
	out := make([]gen.Role, 0, len(roles))

	for _, r := range roles {
		perms := r.Permissions
		if perms == nil {
			perms = []string{}
		}

		out = append(out, gen.Role{Code: r.Code, Name: r.Name, Permissions: perms})
	}

	return gen.ListRoles200JSONResponse{Code: response.Code, Message: response.Message, Data: out}, nil
}

// ListPermissions handles GET /api/v1/permissions.
func (s *Server) ListPermissions(context.Context, gen.ListPermissionsRequestObject) (gen.ListPermissionsResponseObject, error) {
	codes := entity.Permissions()
	out := make([]gen.Permission, 0, len(codes))

	for _, code := range codes {
		out = append(out, gen.Permission{Code: code})
	}

	return gen.ListPermissions200JSONResponse{Code: response.Code, Message: response.Message, Data: out}, nil
}

// DeleteUser handles DELETE /api/v1/users/{id}.
func (s *Server) DeleteUser(ctx context.Context, req gen.DeleteUserRequestObject) (gen.DeleteUserResponseObject, error) {
	if err := s.accounts.Delete(ctx, req.Id); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	return gen.DeleteUser202JSONResponse{Code: response.Code, Message: response.Message, Data: nil}, nil
}
