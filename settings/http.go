package settings

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"swiftbon.com/shared/principal"
)

// Response codes of the internal settings API. They follow the house style:
// business failures are HTTP 200 with a code, and "00" is success. The new codes
// are strings, not reused numbers, so a caller can tell a bad value from a
// missing approval from a dead database.
const (
	CodeSuccess          = "00"
	CodeValidation       = "02"
	CodeNotFound         = "06"
	CodeInvalidValue     = "SETTING_INVALID_VALUE"
	CodeNotRegistered    = "SETTING_NOT_REGISTERED"
	CodeScope            = "SETTING_SCOPE"
	CodeApprovalRequired = "SETTING_APPROVAL_REQUIRED"
	CodeInternal         = "99"
)

// Result is the response body, shaped like the estate's BaseResponse.
type Result struct {
	ResponseCode        string `json:"responseCode"`
	ResponseDescription string `json:"responseDescription"`
	ResponseDetails     any    `json:"responseDetails,omitempty"`
}

type changeBody struct {
	Value             json.RawMessage `json:"value"`
	Reason            string          `json:"reason"`
	OrgID             *uuid.UUID      `json:"org_id"`
	ApprovalRequestID *uuid.UUID      `json:"approval_request_id"`
	ChangedBy         string          `json:"changed_by"`
	EffectiveFrom     time.Time       `json:"effective_from"`
}

// Mount registers the internal settings API on g, so a service mounts it in one
// line:
//
//	settings.Mount(ops.Group("/settings", principal.Require(v, principal.WithRealm(principal.Staff))), mgr)
//
// Routes (relative to g):
//
//	GET  ""                list every key at its global value
//	GET  "/:key"           one key, its history; ?org_id= for an override
//	POST "/:key/propose"   {value, reason, org_id?} validate only; writes nothing
//	POST "/:key/apply"     {value, reason, org_id?, approval_request_id, changed_by?}
//
// Authentication is the caller's: put principal.Require (staff realm) on g.
// Apply is meant to be called by the approval flow once a setting_change is
// approved, never straight from a browser.
func Mount(g *echo.Group, m *Manager) {
	g.GET("", func(c echo.Context) error {
		list, err := m.List(c.Request().Context())
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, Result{CodeSuccess, "Success", list})
	})

	g.GET("/:key", func(c echo.Context) error {
		org, err := orgQuery(c)
		if err != nil {
			return c.JSON(http.StatusOK, Result{ResponseCode: CodeValidation, ResponseDescription: "org_id is not a UUID"})
		}
		d, err := m.Inspect(c.Request().Context(), c.Param("key"), org)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, Result{CodeSuccess, "Success", d})
	})

	g.POST("/:key/propose", func(c echo.Context) error {
		ch, err := bindChange(c)
		if err != nil {
			return c.JSON(http.StatusOK, Result{ResponseCode: CodeValidation, ResponseDescription: "Request is in an incorrect manner."})
		}
		p, err := m.Propose(c.Request().Context(), ch)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, Result{CodeSuccess, "Success", p})
	})

	g.POST("/:key/apply", func(c echo.Context) error {
		ch, err := bindChange(c)
		if err != nil {
			return c.JSON(http.StatusOK, Result{ResponseCode: CodeValidation, ResponseDescription: "Request is in an incorrect manner."})
		}
		// The actor is the authenticated staff member when there is one, never a
		// value the body could spoof.
		if p, ok := principal.From(c.Request().Context()); ok {
			ch.ChangedBy = p.UserID.String()
		}
		row, ev, err := m.Apply(c.Request().Context(), ch)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, Result{CodeSuccess, "Success", map[string]any{
			"key": row.Key, "org_id": row.OrgID, "version": row.Version,
			"effective_from": row.EffectiveFrom, "event": ev,
		}})
	})
}

func orgQuery(c echo.Context) (*uuid.UUID, error) {
	raw := c.QueryParam("org_id")
	if raw == "" {
		return nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func bindChange(c echo.Context) (Change, error) {
	var b changeBody
	if err := c.Bind(&b); err != nil {
		return Change{}, err
	}
	return Change{
		Key: c.Param("key"), OrgID: b.OrgID, Value: b.Value, Reason: b.Reason,
		ChangedBy: b.ChangedBy, ApprovalRequestID: b.ApprovalRequestID, EffectiveFrom: b.EffectiveFrom,
	}, nil
}

func fail(c echo.Context, err error) error {
	r := Result{ResponseCode: CodeInternal, ResponseDescription: "Unexpected error occurred."}
	switch {
	case errors.Is(err, ErrNotRegistered):
		r = Result{ResponseCode: CodeNotRegistered, ResponseDescription: err.Error()}
	case errors.Is(err, ErrInvalidValue):
		r = Result{ResponseCode: CodeInvalidValue, ResponseDescription: err.Error()}
	case errors.Is(err, ErrScope):
		r = Result{ResponseCode: CodeScope, ResponseDescription: err.Error()}
	case errors.Is(err, ErrNotApproved):
		r = Result{ResponseCode: CodeApprovalRequired, ResponseDescription: err.Error()}
	}
	return c.JSON(http.StatusOK, r)
}
