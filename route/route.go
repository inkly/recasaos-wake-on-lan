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
	"sync"
	"time"

	"github.com/ReCasaOS/CasaOS-Common/external"
	"github.com/ReCasaOS/CasaOS-Common/utils/jwt"
	"github.com/ReCasaOS/CasaOS-Common/utils/logger"
	"github.com/inkly/recasaos-wake-on-lan/common"
	"github.com/inkly/recasaos-wake-on-lan/pkg/config"
	"github.com/inkly/recasaos-wake-on-lan/service"
	echojwt "github.com/labstack/echo-jwt/v4"
	"github.com/labstack/echo/v4"
	echo_middleware "github.com/labstack/echo/v4/middleware"
	"go.uber.org/zap"
)

const (
	// APIPath is the route the module registers with the gateway (gateway_route of module.json).
	APIPath = "/v1/wake_on_lan"
	// DocPath is the route of the description of the API, served at DocPath/openapi.yaml.
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
	// Now is the clock of the limit of one wake a second for a MAC address.
	Now func() time.Time
}

type message struct {
	Message string `json:"message"`
}

func isInternal(c echo.Context) bool {
	return external.IsInternalRequest(c.RealIP(), c.Request().Header.Get(echo.HeaderAuthorization), config.CommonInfo.RuntimePath)
}

// mayEditKey is where liveAccount leaves whether the caller may change the list of devices.
const mayEditKey = "may_edit"

// liveAccount refuses an account that user-service says was removed, on every route behind
// the token, and answers 503 while the role cannot be read, as the box's other services do.
// The box's own services pass. It notes whether the caller is the administrator.
func liveAccount(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if isInternal(c) {
			c.Set(mayEditKey, true)
			return next(c)
		}
		role, gone, known := account(config.CommonInfo.RuntimePath, c.Request().Header.Get("user_id"))
		if !known {
			return c.JSON(http.StatusServiceUnavailable, message{"the account's role cannot be read for now"})
		}
		if gone {
			return c.JSON(http.StatusForbidden, message{"this account was removed"})
		}
		c.Set(mayEditKey, role == "admin")
		return next(c)
	}
}

func mayEdit(c echo.Context) bool {
	may, _ := c.Get(mayEditKey).(bool)
	return may
}

// adminOnly lets through the box's own services and the administrator: no other account
// changes the list of devices.
func adminOnly(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if !mayEdit(c) {
			return c.JSON(http.StatusForbidden, message{"only the administrator changes the list of devices"})
		}
		return next(c)
	}
}

// wakeLimit lets one wake a second through for each MAC address. It holds only saved MACs,
// so at most MaxDevices entries and a few more after a save.
type wakeLimit struct {
	mu   sync.Mutex
	now  func() time.Time
	last map[string]time.Time
}

func (l *wakeLimit) allow(mac string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if at, ok := l.last[mac]; ok && now.Sub(at) < time.Second {
		return false
	}
	l.last[mac] = now
	return true
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

	e.Use(liveAccount)

	e.Use(echo_middleware.BodyLimit("64K"))

	now := deps.Now
	if now == nil {
		now = time.Now
	}
	limit := &wakeLimit{now: now, last: map[string]time.Time{}}

	e.GET(APIPath+"/status", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"version": common.Version})
	})

	e.GET(APIPath+"/devices", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{"devices": deps.Store.List(), "can_edit": mayEdit(c)})
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
		if !limit.allow(mac) {
			c.Response().Header().Set("Retry-After", "1")
			return c.JSON(http.StatusTooManyRequests, message{"one wake a second for a device"})
		}
		if err := deps.Wake(mac); err != nil {
			logger.Error("the magic packet could not be sent", zap.String("mac", mac), zap.Error(err))
			return c.JSON(http.StatusInternalServerError, message{"the packet could not be sent"})
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
func InitDocRouter(docYAML string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != DocPath+"/openapi.yaml" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = io.WriteString(w, docYAML)
	})
}
