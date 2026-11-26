package route

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ReCasaOS/CasaOS-Common/external"
	"github.com/ReCasaOS/CasaOS-Common/utils/jwt"
	"github.com/ReCasaOS/CasaOS-Common/utils/logger"
	"github.com/inkly/casaos-wake-on-lan/pkg/config"
	"github.com/inkly/casaos-wake-on-lan/service"
)

// The common library keeps user-service's public key for ten seconds whatever the runtime
// path, so every test of this binary checks tokens against the same key, served by the same
// stand-in. stranger signs with a key the box does not know: another service's token.
var signer = sync.OnceValues(func() (address string, sign func(stranger bool) (string, error)) {
	privateKey, publicKey, err := jwt.GenerateKeyPair()
	if err != nil {
		panic(err)
	}
	otherKey, _, err := jwt.GenerateKeyPair()
	if err != nil {
		panic(err)
	}
	jwks, err := jwt.GenerateJwksJSON(publicKey)
	if err != nil {
		panic(err)
	}

	return httptest.NewServer(jwt.JWKSHandler(jwks)).URL, func(stranger bool) (string, error) {
		if stranger {
			return jwt.GetAccessToken("someone", otherKey, 1)
		}
		return jwt.GetAccessToken("someone", privateKey, 1)
	}
})

const secret = "s3cret"

// box is a runtime path with an internal secret and a user-service that signs tokens; it
// returns a valid token of account 1, whose role is what the test says.
func box(t *testing.T, role string, known bool) (token string) {
	t.Helper()
	logger.LogInitConsoleOnly()
	runtime := t.TempDir()
	previous := config.CommonInfo.RuntimePath
	config.CommonInfo.RuntimePath = runtime
	previousAccount := account
	account = func(string, string) (string, bool, bool) { return role, false, known }
	t.Cleanup(func() { config.CommonInfo.RuntimePath = previous; account = previousAccount })

	if err := os.WriteFile(filepath.Join(runtime, external.InternalSecretFilename), []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	address, sign := signer()
	if err := os.WriteFile(filepath.Join(runtime, external.UserServiceAddressFilename), []byte(address), 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := sign(false)
	if err != nil {
		t.Fatal(err)
	}

	return token
}

// fixture is a router over a store with one saved device, and the MACs it was asked to wake.
type fixture struct {
	handler http.Handler
	store   *service.Store
	woken   []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{store: service.OpenStore(filepath.Join(t.TempDir(), "devices.json"), func(string) {})}
	if err := f.store.Replace([]service.Device{{Name: "Desk PC", MAC: "aa:bb:cc:dd:ee:01"}}); err != nil {
		t.Fatal(err)
	}
	f.handler = InitRouter(Deps{Store: f.store, Wake: func(mac string) error { f.woken = append(f.woken, mac); return nil }, Page: "<!DOCTYPE html><title>page</title>"})
	return f
}

func (f *fixture) send(method, path, authorization, body string, from string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	request.RemoteAddr = from
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)
	return recorder
}

const lan = "203.0.113.5:40000"

func TestEveryRouteNeedsAToken(t *testing.T) {
	box(t, "admin", true)
	_, sign := signer()
	stranger, err := sign(true)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t)
	calls := []struct{ method, path, body string }{
		{http.MethodGet, "/v1/wake_on_lan/devices", ""},
		{http.MethodPut, "/v1/wake_on_lan/devices", `{"devices":[]}`},
		{http.MethodPost, "/v1/wake_on_lan/wake", `{"mac":"aa:bb:cc:dd:ee:01"}`},
		{http.MethodGet, "/v1/wake_on_lan/status", ""},
	}
	for _, c := range calls {
		for what, authorization := range map[string]string{
			"no token":                     "",
			"another service's token":      stranger,
			"another service's, as Bearer": "Bearer " + stranger,
			"not a token":                  "Bearer x.y.z",
			"internal secret from the LAN": "Internal " + secret,
		} {
			if r := f.send(c.method, c.path, authorization, c.body, lan); r.Code != http.StatusUnauthorized {
				t.Errorf("%s %s with %s: %d, want 401", c.method, c.path, what, r.Code)
			}
		}
	}
	if len(f.woken) != 0 || len(f.store.List()) != 1 {
		t.Errorf("woken %v, devices %v: a refused call changed something", f.woken, f.store.List())
	}
}

func TestOnlyTheAdministratorSavesTheDevices(t *testing.T) {
	body := `{"devices":[{"name":"NAS","mac":"AA-BB-CC-DD-EE-09"}]}`
	for _, c := range []struct {
		role  string
		known bool
		code  int
	}{
		{"user", true, http.StatusForbidden},
		{"user", false, http.StatusServiceUnavailable},
		{"admin", true, http.StatusOK},
	} {
		token := box(t, c.role, c.known)
		f := newFixture(t)
		r := f.send(http.MethodPut, "/v1/wake_on_lan/devices", token, body, lan)
		if r.Code != c.code {
			t.Errorf("role %q known %v: %d, want %d", c.role, c.known, r.Code, c.code)
		}
		saved := f.store.List()
		if c.code == http.StatusOK && (len(saved) != 1 || saved[0].MAC != "aa:bb:cc:dd:ee:09") {
			t.Errorf("admin: saved %v", saved)
		}
		if c.code != http.StatusOK && (len(saved) != 1 || saved[0].MAC != "aa:bb:cc:dd:ee:01") {
			t.Errorf("role %q: the list changed to %v", c.role, saved)
		}
	}

	// the box's own services, from loopback with the secret
	box(t, "user", false)
	f := newFixture(t)
	if r := f.send(http.MethodPut, "/v1/wake_on_lan/devices", "Internal "+secret, body, "127.0.0.1:5000"); r.Code != http.StatusOK {
		t.Errorf("internal: %d %s", r.Code, r.Body)
	}
}

func TestDevicesSayWhoMayEdit(t *testing.T) {
	for role, want := range map[string]bool{"admin": true, "user": false} {
		token := box(t, role, true)
		r := newFixture(t).send(http.MethodGet, "/v1/wake_on_lan/devices", token, "", lan)
		var got struct {
			Devices []service.Device `json:"devices"`
			CanEdit bool             `json:"can_edit"`
		}
		if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &got) != nil {
			t.Fatalf("%s: %d %s", role, r.Code, r.Body)
		}
		if got.CanEdit != want || len(got.Devices) != 1 || got.Devices[0].Name != "Desk PC" {
			t.Errorf("%s: %+v", role, got)
		}
	}
}

func TestSavingRefusesWhatIsNotValid(t *testing.T) {
	token := box(t, "admin", true)
	var many []string
	for i := 0; i < 65; i++ {
		many = append(many, fmt.Sprintf(`{"name":"pc %d","mac":"aa:bb:cc:dd:ee:%02x"}`, i, i))
	}
	for what, c := range map[string]struct {
		body string
		code int
	}{
		"65 devices":      {`{"devices":[` + strings.Join(many, ",") + `]}`, http.StatusBadRequest},
		"300 characters":  {`{"devices":[{"name":"` + strings.Repeat("a", 300) + `","mac":"aa:bb:cc:dd:ee:01"}]}`, http.StatusBadRequest},
		"seven bytes":     {`{"devices":[{"name":"pc","mac":"aa:bb:cc:dd:ee:01:02"}]}`, http.StatusBadRequest},
		"unknown field":   {`{"devices":[],"owner":"me"}`, http.StatusBadRequest},
		"not JSON":        {`{"devices":`, http.StatusBadRequest},
		"no list":         {`{}`, http.StatusBadRequest},
		"two values":      {`{"devices":[]}{"devices":[]}`, http.StatusBadRequest},
		"more than 64KiB": {`{"devices":[],"pad":"` + strings.Repeat("a", 70<<10) + `"}`, http.StatusRequestEntityTooLarge},
	} {
		f := newFixture(t)
		if r := f.send(http.MethodPut, "/v1/wake_on_lan/devices", token, c.body, lan); r.Code != c.code {
			t.Errorf("%s: %d, want %d", what, r.Code, c.code)
		}
		if l := f.store.List(); len(l) != 1 || l[0].Name != "Desk PC" {
			t.Errorf("%s: the list changed to %v", what, l)
		}
	}
}

func TestWakeOnlyASavedDevice(t *testing.T) {
	token := box(t, "user", true)
	f := newFixture(t)
	for what, c := range map[string]struct {
		body string
		code int
	}{
		"saved, other spelling": {`{"mac":"AA-BB-CC-DD-EE-01"}`, http.StatusOK},
		"not saved":             {`{"mac":"aa:bb:cc:dd:ee:02"}`, http.StatusNotFound},
		"seven bytes":           {`{"mac":"aa:bb:cc:dd:ee:01:02"}`, http.StatusBadRequest},
		"an address too":        {`{"mac":"aa:bb:cc:dd:ee:01","address":"8.8.8.8"}`, http.StatusBadRequest},
		"no MAC":                {`{}`, http.StatusBadRequest},
		"more than 64KiB":       {`{"mac":"` + strings.Repeat("a", 70<<10) + `"}`, http.StatusRequestEntityTooLarge},
	} {
		if r := f.send(http.MethodPost, "/v1/wake_on_lan/wake", token, c.body, lan); r.Code != c.code {
			t.Errorf("%s: %d %s, want %d", what, r.Code, r.Body, c.code)
		}
	}
	if len(f.woken) != 1 || f.woken[0] != "aa:bb:cc:dd:ee:01" {
		t.Errorf("woken %v, want the saved device once", f.woken)
	}
}

func TestStatus(t *testing.T) {
	token := box(t, "user", true)
	r := newFixture(t).send(http.MethodGet, "/v1/wake_on_lan/status", token, "", lan)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"version":"0.1.0"`) {
		t.Errorf("%d %s", r.Code, r.Body)
	}
}

func TestThePageNeedsNoToken(t *testing.T) {
	box(t, "user", true)
	f := newFixture(t)
	r := f.send(http.MethodGet, "/v1/wake_on_lan/", "", "", lan)
	if r.Code != http.StatusOK || !strings.HasPrefix(r.Header().Get("Content-Type"), "text/html") || !strings.Contains(r.Body.String(), "<title>page</title>") {
		t.Fatalf("%d %q %s", r.Code, r.Header().Get("Content-Type"), r.Body)
	}
	if csp := r.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "connect-src 'self'") || !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("Content-Security-Policy %q", csp)
	}

	r = f.send(http.MethodGet, "/v1/wake_on_lan", "", "", lan)
	if r.Code != http.StatusMovedPermanently || r.Header().Get("Location") != "/v1/wake_on_lan/" {
		t.Errorf("bare route: %d %q", r.Code, r.Header().Get("Location"))
	}

	// only those two paths, and only to read
	if r := f.send(http.MethodPost, "/v1/wake_on_lan/", "", "", lan); r.Code != http.StatusUnauthorized && r.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST on the page: %d", r.Code)
	}
	if r := f.send(http.MethodGet, "/v1/wake_on_lan/index.html", "", "", lan); r.Code != http.StatusUnauthorized {
		t.Errorf("another path without a token: %d", r.Code)
	}
}

func TestDoc(t *testing.T) {
	h := InitDocRouter("<html>doc</html>", "openapi: 3.0.3")
	for path, want := range map[string]string{"/doc/v1/wake_on_lan": "doc", "/doc/v1/wake_on_lan/openapi.yaml": "openapi"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), want) {
			t.Errorf("%s: %d %s", path, r.Code, r.Body)
		}
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/doc/v1/wake_on_lan/other", nil))
	if r.Code != http.StatusNotFound {
		t.Errorf("other doc path: %d", r.Code)
	}
}
