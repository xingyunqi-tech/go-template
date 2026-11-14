package v1

import (
	"context"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/v1/view"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

const (
	_defaultUsersLimit = 10
	_maxUsersLimit     = 50
	_descending        = "DESC"
)

// CreateUser handles POST users.
func (s *Server) CreateUser(ctx context.Context, _ gen.CreateUserRequestObject) (gen.CreateUserResponseObject, error) {
	in, err := userInput(body(ctx))
	if err != nil {
		return nil, err
	}

	u, created, err := s.users.Create(ctx, in)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.CreateUser201JSONResponse(view.NewUser(u, s.urls, &view.Created{
		RoleID: created.RoleID, StatusID: created.StatusID, Photo: created.Photo,
	})), nil
}

func number(o *validatorx.Object, key string, def int) int {
	if v, ok := field(o, key); ok {
		if f, isNumber := v.(float64); isNumber {
			return int(f)
		}
	}

	return def
}

func objectField(o *validatorx.Object, key string) *validatorx.Object {
	v, _ := o.Get(key)
	obj, _ := v.(*validatorx.Object)

	return obj
}

func objectItems(o *validatorx.Object, key string) []*validatorx.Object {
	if o == nil {
		return nil
	}

	v, _ := o.Get(key)

	arr, ok := v.(*validatorx.Array)
	if !ok {
		return nil
	}

	var out []*validatorx.Object

	for _, item := range arr.Items {
		if obj, isObject := item.(*validatorx.Object); isObject {
			out = append(out, obj)
		}
	}

	return out
}

func userList(q *validatorx.Object) *repo.UserList {
	page := number(q, "page", 1)
	limit := min(number(q, "limit", _defaultUsersLimit), _maxUsersLimit)
	list := &repo.UserList{RoleIDs: nil, Sort: nil, Offset: int64((page - 1) * limit), Limit: int64(limit)}

	for _, role := range objectItems(objectField(q, "filters"), "roles") {
		list.RoleIDs = append(list.RoleIDs, number(role, "id", 0))
	}

	positions := map[string]int{}

	for _, s := range objectItems(q, "sort") {
		orderBy, _ := s.Get("orderBy")
		order, _ := s.Get("order")
		field := repo.SortField{Property: validatorx.ToString(orderBy), Descending: validatorx.ToString(order) == _descending}

		if i, seen := positions[field.Property]; seen {
			list.Sort[i] = field

			continue
		}

		positions[field.Property] = len(list.Sort)
		list.Sort = append(list.Sort, field)
	}

	return list
}

// GetUsers handles GET users.
func (s *Server) GetUsers(ctx context.Context, _ gen.GetUsersRequestObject) (gen.GetUsersResponseObject, error) {
	list := userList(query(ctx))

	users, err := s.users.List(ctx, list)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	page := view.UserPage{Data: make([]view.User, len(users)), HasNextPage: int64(len(users)) == list.Limit}
	for i := range users {
		page.Data[i] = view.NewUser(&users[i], s.urls, nil)
	}

	return gen.GetUsers200JSONResponse(page), nil
}

// GetUser handles GET users/:id.
func (s *Server) GetUser(ctx context.Context, req gen.GetUserRequestObject) (gen.GetUserResponseObject, error) {
	u, err := s.users.FindByID(ctx, int(req.Id))
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.GetUser200JSONResponse(view.NewUser(u, s.urls, nil)), nil
}

// UpdateUser handles PUT users/:id.
func (s *Server) UpdateUser(ctx context.Context, req gen.UpdateUserRequestObject) (gen.UpdateUserResponseObject, error) {
	in, err := userInput(body(ctx))
	if err != nil {
		return nil, err
	}

	u, err := s.users.UpdateByAdmin(ctx, int(req.Id), in)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.UpdateUser200JSONResponse(view.NewUser(u, s.urls, nil)), nil
}

// DeleteUser handles DELETE users/:id.
func (s *Server) DeleteUser(ctx context.Context, req gen.DeleteUserRequestObject) (gen.DeleteUserResponseObject, error) {
	if err := s.users.Remove(ctx, int(req.Id)); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.DeleteUser200Response{Body: nil}, nil
}
