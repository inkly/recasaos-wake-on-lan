// Package route answers on the module's route of the gateway: the page, and the API it calls.
package route

import (
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/ReCasaOS/CasaOS-Common/external"
	"github.com/ReCasaOS/CasaOS-Common/utils/jwt"
	"github.com/ReCasaOS/CasaOS-Common/utils/logger"
	"github.com/inkly/casaos-wake-on-lan/common"
	"github.com/inkly/casaos-wake-on-lan/pkg/config"
	"github.com/inkly/casaos-wake-on-lan/service"
	echojwt "github.com/labstack/echo-jwt/v4"
	"github.com/labstack/echo/v4"
	echo_middleware "github.com/labstack/echo/v4/middleware"
	"go.uber.org/zap"
)

const (
	// APIPath is the route the module registers with the gateway (gateway_route of module.json).
	APIPath = "/v1/wake_on_lan"
	// DocPath is where the description of the API is.
	DocPath = "/doc" + APIPath
	// PagePath is the page, which a browser opens without a token.
	PagePath = APIPath + "/"
)

// account is a variable so a test can say what an account is without a user-service.
var account = external.Account

// accessLogFormat is echo's default access log with the path in place of the request URI, so
// that nothing a caller puts in a query string reaches the journal.
var accessLogFormat = strings.Replace(echo_middleware.DefaultLoggerConfig.Format, `"uri":"${uri}"`, `"path":"${path}"`, 1)

// pageHeaders keep the page to itself: it loads nothing and calls nothing but this box, so a
// mistake in it cannot send the dashboard's token anywhere else.
var pageHeaders = map[string]string{
	"Content-Type":            "text/html; charset=utf-8",
	"Content-Security-Policy": "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'",
	"X-Content-Type-Options":  "nosniff",
	"Referrer-Policy":         "no-referrer",
	"Cache-Control":           "no-cache",
}

// Deps is what the routes are built on.
type Deps struct {
	// Store holds the saved devices.
	Store *service.Store
	// Wake sends the magic packet of a MAC address, in its one form.
	Wake func(mac string) error
	// Page is the HTML of the page.
	Page string
}

type message struct {
	Message string `json:"message"`
}

func isInternal(c echo.Context) bool {
	return external.IsInternalRequest(c.RealIP(), c.Request().Header.Get(echo.HeaderAuthorization), config.CommonInfo.RuntimePath)
}

// mayEdit says whether the caller may change the list of devices: the box's own services and
// the administrator. known is false when the account's role cannot be read for now.
func mayEdit(c echo.Context) (may, known bool) {
	if isInternal(c) {
		return true, true
	}
	role, gone, known := account(config.CommonInfo.RuntimePath, c.Request().Header.Get("user_id"))
	return known && role == "admin" && !gone, known
}

// adminOnly lets through the box's own services and the administrator: no other account
// changes the list of devices.
func adminOnly(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		may, known := mayEdit(c)
		if !known {
			return c.JSON(http.StatusServiceUnavailable, message{"the account's role cannot be read for now"})
		}
		if !may {
			return c.JSON(http.StatusForbidden, message{"only the administrator changes the list of devices"})
		}
		return next(c)
	}
}

// decode reads one JSON value into v, with no field v does not have and nothing after it.
func decode(c echo.Context, v any) error {
	notTaken := echo.NewHTTPError(http.StatusBadRequest, message{"the body is not the JSON this route takes"})
	d := json.NewDecoder(c.Request().Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		var tooLarge *echo.HTTPError // (BodyLimit's, when the body is longer than it said)
		if errors.As(err, &tooLarge) {
			return tooLarge
		}
		return notTaken
	}
	if _, err := d.Token(); err != io.EOF {
		return notTaken
	}
	return nil
}

type devicesBody struct {
	Devices *[]service.Device `json:"devices"`
}

type wakeBody struct {
	MAC string `json:"mac"`
}

// InitRouter answers on APIPath: the page without a token, every other path with the
// dashboard's token (or as one of the box's own services).
func InitRouter(deps Deps) http.Handler {
	e := echo.New()

	e.Use(echo_middleware.LoggerWithConfig(echo_middleware.LoggerConfig{Format: accessLogFormat}))

	e.Use(echojwt.WithConfig(echojwt.Config{
		// loopback AND the internal secret: loopback alone is not an identity
		Skipper: isInternal,
		ParseTokenFunc: func(c echo.Context, token string) (interface{}, error) {
			valid, claims, err := jwt.Validate(token, func() (*ecdsa.PublicKey, error) { return external.GetPublicKey(config.CommonInfo.RuntimePath) })
			if err != nil || !valid {
				return nil, echo.ErrUnauthorized
			}

			c.Request().Header.Set("user_id", strconv.Itoa(claims.ID))

			return claims, nil
		},
		TokenLookupFuncs: []echo_middleware.ValuesExtractor{
			// the header only, the token in it bare or `Bearer <token>`
			func(c echo.Context) ([]string, error) {
				return []string{jwt.TokenFromAuthorization(c.Request().Header.Get(echo.HeaderAuthorization))}, nil
			},
		},
	}))

	e.Use(echo_middleware.BodyLimit("64K"))

	e.GET(APIPath+"/status", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"version": common.Version})
	})

	e.GET(APIPath+"/devices", func(c echo.Context) error {
		may, _ := mayEdit(c)
		return c.JSON(http.StatusOK, map[string]any{"devices": deps.Store.List(), "can_edit": may})
	})

	e.PUT(APIPath+"/devices", func(c echo.Context) error {
		var body devicesBody
		if err := decode(c, &body); err != nil {
			return err
		}
		if body.Devices == nil {
			return c.JSON(http.StatusBadRequest, message{"the body has no list of devices"})
		}
		list, err := service.CheckDevices(*body.Devices)
		if err != nil {
			return c.JSON(http.StatusBadRequest, message{err.Error()})
		}
		if err := deps.Store.Replace(list); err != nil {
			logger.Error("the devices could not be saved", zap.Error(err))
			return c.JSON(http.StatusInternalServerError, message{"the devices could not be saved"})
		}
		return c.JSON(http.StatusOK, map[string]any{"devices": deps.Store.List(), "can_edit": true})
	}, adminOnly)

	e.POST(APIPath+"/wake", func(c echo.Context) error {
		var body wakeBody
		if err := decode(c, &body); err != nil {
			return err
		}
		mac, err := service.ParseMAC(body.MAC)
		if err != nil {
			return c.JSON(http.StatusBadRequest, message{err.Error()})
		}
		// only a saved device: the route is not a sender of packets to any address
		if !deps.Store.Has(mac) {
			return c.JSON(http.StatusNotFound, message{"no saved device has this MAC address"})
		}
		if err := deps.Wake(mac); err != nil {
			logger.Error("the magic packet could not be sent", zap.String("mac", mac), zap.Error(err))
			return c.JSON(http.StatusInternalServerError, message{"the magic packet could not be sent: " + err.Error()})
		}
		return c.JSON(http.StatusOK, message{"sent"})
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			switch r.URL.Path {
			case APIPath:
				http.Redirect(w, r, PagePath, http.StatusMovedPermanently)
				return
			case PagePath:
				for k, v := range pageHeaders {
					w.Header().Set(k, v)
				}
				_, _ = io.WriteString(w, deps.Page)
				return
			}
		}

		e.ServeHTTP(w, r)
	})
}

// InitDocRouter serves the description of the API, without a token, as the other services of
// the box do.
func InitDocRouter(docHTML string, docYAML string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case DocPath:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, docHTML)
		case DocPath + "/openapi.yaml":
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = io.WriteString(w, docYAML)
		default:
			http.NotFound(w, r)
		}
	})
}
