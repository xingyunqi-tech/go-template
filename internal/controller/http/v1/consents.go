package v1

import (
	"context"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/v1/view"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

// GetMyConsents handles GET me/consents.
func (s *Server) GetMyConsents(ctx context.Context, _ gen.GetMyConsentsRequestObject) (gen.GetMyConsentsResponseObject, error) {
	statuses, err := s.consents.Statuses(ctx, reqctx.Principal(ctx).UserID)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.GetMyConsents200JSONResponse(view.NewConsentStatuses(statuses)), nil
}

// AgreeConsent handles POST me/consents.
func (s *Server) AgreeConsent(ctx context.Context, _ gen.AgreeConsentRequestObject) (gen.AgreeConsentResponseObject, error) {
	b := body(ctx)
	consentType, _ := field(b, "type")
	version, _ := field(b, "version")

	status, err := s.consents.Agree(
		ctx, reqctx.Principal(ctx).UserID, validatorx.ToString(consentType), validatorx.ToString(version), reqctx.Client(ctx).IP,
	)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.AgreeConsent200JSONResponse(view.NewConsentStatus(status)), nil
}
