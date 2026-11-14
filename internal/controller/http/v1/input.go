package v1

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase/user"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

const _dateLength = 10

var errNotInteger = errors.New("not an integer")

func body(ctx context.Context) *validatorx.Object {
	o, _ := reqctx.ValidatedValues(ctx).Body.(*validatorx.Object)
	if o == nil {
		return validatorx.NewObject()
	}

	return o
}

func query(ctx context.Context) *validatorx.Object {
	o, _ := reqctx.ValidatedValues(ctx).Query.(*validatorx.Object)
	if o == nil {
		return validatorx.NewObject()
	}

	return o
}

func field(o *validatorx.Object, key string) (validatorx.Value, bool) {
	v, ok := o.Get(key)
	if !ok {
		return nil, false
	}

	if _, undefined := v.(validatorx.Undefined); undefined {
		return nil, false
	}

	return v, true
}

func optString(o *validatorx.Object, key string) repo.Optional[string] {
	v, ok := field(o, key)

	switch {
	case !ok:
		return repo.Optional[string]{Set: false, Value: nil}
	case v == nil:
		return repo.Null[string]()
	default:
		return repo.Some(validatorx.ToString(v))
	}
}

func stringPtr(o *validatorx.Object, key string) *string {
	return optString(o, key).Value
}

func optInt(o *validatorx.Object, key string) (repo.Optional[int], error) {
	v, ok := field(o, key)

	switch {
	case !ok:
		return repo.Optional[int]{Set: false, Value: nil}, nil
	case v == nil:
		return repo.Null[int](), nil
	}

	f, isNumber := v.(float64)
	if !isNumber {
		parsed, err := strconv.ParseFloat(validatorx.ToString(v), 64)
		if err != nil {
			return repo.Optional[int]{Set: false, Value: nil}, fmt.Errorf("v1: %s: %w", key, err)
		}

		f = parsed
	}

	if f != math.Trunc(f) || math.IsInf(f, 0) {
		return repo.Optional[int]{Set: false, Value: nil}, fmt.Errorf("v1: %s: %w", key, errNotInteger)
	}

	return repo.Some(int(f)), nil
}

func optDate(o *validatorx.Object, key string) (repo.Optional[time.Time], error) {
	v, ok := field(o, key)

	switch {
	case !ok:
		return repo.Optional[time.Time]{Set: false, Value: nil}, nil
	case v == nil:
		return repo.Null[time.Time](), nil
	}

	s := validatorx.ToString(v)
	if len(s) > _dateLength {
		s = s[:_dateLength]
	}

	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return repo.Optional[time.Time]{Set: false, Value: nil}, fmt.Errorf("v1: %s: %w", key, err)
	}

	return repo.Some(d), nil
}

func truthy(v validatorx.Value) bool {
	switch x := v.(type) {
	case nil, validatorx.Undefined:
		return false
	case bool:
		return x
	case float64:
		return x != 0 && !math.IsNaN(x)
	case string:
		return x != ""
	default:
		return true
	}
}

func ref(o *validatorx.Object, key string) user.Ref {
	v, ok := field(o, key)

	switch {
	case !ok:
		return user.Ref{Set: false, Null: false, ID: ""}
	case v == nil:
		return user.Ref{Set: true, Null: true, ID: ""}
	}

	r := user.Ref{Set: true, Null: false, ID: ""}

	if obj, isObject := v.(*validatorx.Object); isObject {
		if id, has := obj.Get("id"); has && truthy(id) {
			r.ID = validatorx.ToString(id)
		}
	}

	return r
}

// userInput converts a validated user DTO; fields absent from the request stay unset.
func userInput(o *validatorx.Object) (*user.Input, error) {
	gender, err := optInt(o, "gender")
	if err != nil {
		return nil, err
	}

	birthday, err := optDate(o, "birthday")
	if err != nil {
		return nil, err
	}

	return &user.Input{
		Email: optString(o, "email"), Password: optString(o, "password"), Provider: optString(o, "provider"),
		FirstName: optString(o, "firstName"), LastName: optString(o, "lastName"), Phone: optString(o, "phone"),
		Nickname: optString(o, "nickname"), Gender: gender, Birthday: birthday,
		Photo: ref(o, "photo"), Role: ref(o, "role"), Status: ref(o, "status"),
		WechatOpenID: optString(o, "wechatOpenId"), WechatUnionID: optString(o, "wechatUnionId"),
		AlipayOpenID: optString(o, "alipayOpenId"),
	}, nil
}
