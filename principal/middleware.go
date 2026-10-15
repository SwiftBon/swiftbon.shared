package principal

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
)

// Response is the body of a refusal. Its JSON field names match the estate's
// BaseResponse so existing clients read it unchanged; the shared module cannot
// import BaseResponse itself.
type Response struct {
	ResponseCode        string `json:"responseCode"`
	ResponseDescription string `json:"responseDescription"`
}

// Require verifies the X-Swiftbon-Principal header and enforces opts. On
// success the principal is on the request context (see From). A missing or
// invalid token gets 401 UNAUTHENTICATED; a valid token that lacks what the
// route needs gets 403 FORBIDDEN, with the reason in responseDescription.
//
// The organisation a handler works on comes from the principal, never from the
// URL or body.
func Require(v Verifier, opts ...Option) echo.MiddlewareFunc {
	req := newRequirements(opts)
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			p, err := v.Verify(c.Request().Header.Get(HeaderName))
			if err != nil {
				return c.JSON(http.StatusUnauthorized, Response{CodeUnauthenticated, ReasonUnauthorised})
			}
			if err := req.check(p); err != nil {
				return c.JSON(http.StatusForbidden, Response{CodeForbidden, reasonOf(err)})
			}
			c.SetRequest(c.Request().WithContext(WithContext(c.Request().Context(), p)))
			return next(c)
		}
	}
}

func reasonOf(err error) string {
	var az *authzError
	if errors.As(err, &az) {
		return az.reason
	}
	return ""
}
