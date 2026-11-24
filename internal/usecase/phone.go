package usecase

import (
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/pkg/phone"
)

// NormalizePhone returns the E.164 form of raw, or a 422 on field.
func NormalizePhone(field, raw string) (string, error) {
	e164, err := phone.Normalize(raw)
	if err != nil {
		return "", entity.Invalid(field, entity.RuleFormat, map[string]any{"format": "phone"})
	}

	return e164, nil
}
