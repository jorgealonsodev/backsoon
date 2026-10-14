package web

import (
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/jorgealonsodev/backsoon/admin/internal/pages"
)

const langCookie = "backsoon_lang"

// --- helpers -------------------------------------------------------------------------------

func withLang(r *http.Request, lang string) *http.Request {
	if lang != "" {
		r.AddCookie(&http.Cookie{Name: langCookie, Value: lang})
	}
	return r
}

func (hn *harness) getL(path string, s session, lang string) *httptest.ResponseRecorder {
	hn.t.Helper()
	return hn.serve(withLang(s.apply(navigate(httptest.NewRequest(http.MethodGet, path, nil))), lang))
}

func (hn *harness) postFormL(path string, s session, form url.Values, lang string) *httptest.ResponseRecorder {
	hn.t.Helper()
	if _, set := form["csrf"]; !set && s.csrf != "" {
		form.Set("csrf", s.csrf)
	} else if form.Get("csrf") == "-" {
		form.Del("csrf")
	}
	r := navigate(httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode())))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return hn.serve(withLang(s.apply(r), lang))
}

func (hn *harness) postMultipartL(path string, s session, fields map[string]string, file *upload, lang string) *httptest.ResponseRecorder {
	hn.t.Helper()
	body, ctype := multipartBody(hn.t, s, fields, file)
	r := navigate(httptest.NewRequest(http.MethodPost, path, body))
	r.Header.Set("Content-Type", ctype)
	return hn.serve(withLang(s.apply(r), lang))
}

// postLoginL signs in (or fails to) with the language cookie set on both requests.
func (hn *harness) postLoginL(lang, user, password, ip string) *httptest.ResponseRecorder {
	hn.t.Helper()
	g := navigate(httptest.NewRequest(http.MethodGet, "/login", nil))
	g.Header.Set("X-Real-IP", ip)
	w := hn.serve(withLang(g, lang))
	var tok *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "backsoon_login" {
			tok = c
		}
	}
	field := hiddenValue(regexp.MustCompile(`name="login_token" value="([^"]+)"`), w.Body.String())
	form := url.Values{"user": {user}, "password": {password}, "login_token": {field}}
	r := navigate(httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode())))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Real-IP", ip)
	r.AddCookie(&http.Cookie{Name: tok.Name, Value: tok.Value})
	return hn.serve(withLang(r, lang))
}

var (
	htmlLangRe = regexp.MustCompile(`<html lang="([^"]*)"`)
	stripTagRe = regexp.MustCompile(`(?s)<[^>]*>`)
	textAttrRe = regexp.MustCompile(`(?:aria-label|title|placeholder)="([^"]*)"`)
	wordRe     = regexp.MustCompile(`[A-Za-z]+`)
	langLinkRe = regexp.MustCompile(`href="(/lang\?[^"]+)"`)
	textareaRe = regexp.MustCompile(`(?s)<textarea[^>]*>.*?</textarea>`)
)

func pageLang(body string) string {
	if m := htmlLangRe.FindStringSubmatch(body); m != nil {
		return m[1]
	}
	return ""
}

// englishWords are common English words that never belong in Spanish panel copy (page names and
// destinations used by these tests avoid them).
var englishWords = map[string]bool{
	"the": true, "and": true, "you": true, "your": true, "this": true, "with": true, "for": true, "not": true,
	"from": true, "page": true, "pages": true, "destination": true, "destinations": true, "delete": true,
	"save": true, "cancel": true, "back": true, "warning": true, "file": true, "message": true, "name": true,
	"sign": true, "password": true, "user": true, "default": true, "preview": true, "something": true,
	"went": true, "wrong": true, "try": true, "again": true, "expired": true, "created": true, "saved": true,
}

// leftoverEnglish returns the English words found in the text a reader (or a screen reader) gets
// from body: text nodes plus aria-label, title and placeholder attributes.
func leftoverEnglish(body string) []string {
	// Data is not panel copy: what was typed into a field.
	body = styleRe.ReplaceAllString(body, "")
	body = textareaRe.ReplaceAllString(body, "<textarea></textarea>")
	var texts []string
	for _, m := range textAttrRe.FindAllStringSubmatch(body, -1) {
		texts = append(texts, m[1])
	}
	texts = append(texts, stripTagRe.ReplaceAllString(body, " "))
	var found []string
	for _, w := range wordRe.FindAllString(html.UnescapeString(strings.Join(texts, " ")), -1) {
		if englishWords[strings.ToLower(w)] {
			found = append(found, w)
		}
	}
	return found
}

func mustBeSpanish(t *testing.T, name string, w *httptest.ResponseRecorder, want ...string) {
	t.Helper()
	body := w.Body.String()
	if got := pageLang(body); got != "es" {
		t.Errorf("%s: html lang = %q, want es", name, got)
	}
	mustContain(t, body, want...)
	mustNotContainRaw(t, body, "⟦")
	if left := leftoverEnglish(body); len(left) > 0 {
		t.Errorf("%s: English words left in Spanish view: %v\n%s", name, left, excerpt(body))
	}
	if vary := w.Header().Get("Vary"); vary != "Accept-Language, Cookie" {
		t.Errorf("%s: Vary = %q", name, vary)
	}
}

// --- S2: language resolution ---------------------------------------------------------------

func TestLanguageResolution(t *testing.T) {
	hn := newHarness(t)
	cases := []struct {
		name, cookie, accept, want string
	}{
		{"no signal is English", "", "", "en"},
		{"es cookie", "es", "", "es"},
		{"en cookie", "en", "", "en"},
		{"cookie beats Accept-Language (en over es)", "en", "es", "en"},
		{"cookie beats Accept-Language (es over en)", "es", "en-US,en;q=0.9", "es"},
		{"invalid cookie ignored", "fr", "es", "es"},
		{"upper-case cookie is not valid", "ES", "", "en"},
		{"Accept-Language es", "", "es", "es"},
		{"region subtag es-ES", "", "es-ES", "es"},
		{"region subtag es-419 with q", "", "es-419;q=0.8, en;q=0.4", "es"},
		{"mixed case", "", "ES-mx", "es"},
		{"q-values: en wins", "", "es;q=0.3, en;q=0.7", "en"},
		{"q-values: es wins", "", "en;q=0.2, es;q=0.9", "es"},
		{"typical browser en first", "", "en-US,en;q=0.9,es;q=0.8", "en"},
		{"typical browser es first", "", "es-ES,es;q=0.9,en;q=0.8", "es"},
		{"equal q keeps header order", "", "es, en", "es"},
		{"unsupported skipped", "", "fr-FR,fr;q=0.9,es;q=0.5", "es"},
		{"only unsupported", "", "fr,de;q=0.9", "en"},
		{"q=0 excludes", "", "es;q=0, en;q=0.1", "en"},
		{"q=0 only", "", "es;q=0", "en"},
		{"wildcard alone", "", "*", "en"},
		{"garbage", "", ";;;,,, q=1", "en"},
		{"bad q ignored", "", "es;q=abc", "en"},
		{"spaces", "", "  fr ,  es ; q=0.7 ", "es"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := navigate(httptest.NewRequest(http.MethodGet, "/login", nil))
			if tc.accept != "" {
				r.Header.Set("Accept-Language", tc.accept)
			}
			w := hn.serve(withLang(r, tc.cookie))
			wantStatus(t, w, 200)
			body := w.Body.String()
			if got := pageLang(body); got != tc.want {
				t.Fatalf("html lang = %q, want %q", got, tc.want)
			}
			wantText := "Sign in to backsoon"
			if tc.want == "es" {
				wantText = "Inicia sesión en backsoon"
			}
			mustContain(t, body, wantText)
		})
	}
}

func TestVaryOnLocalizedResponses(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	const vary = "Accept-Language, Cookie"
	site := hn.createPage("Alpha", "app.test")
	frame := navigate(httptest.NewRequest(http.MethodGet, "/pages/"+site.ID+"/preview", nil))
	resp := map[string]*httptest.ResponseRecorder{
		"login":      hn.serve(navigate(httptest.NewRequest(http.MethodGet, "/login", nil))),
		"directory":  hn.get("/", s),
		"404":        hn.get("/pages/none", s),
		"form":       hn.get("/pages/new", s),
		"frame":      hn.serve(frame),
		"plain 401":  hn.serve(fetchReq(httptest.NewRequest(http.MethodGet, "/", nil))),
		"login fail": hn.postLogin(testUser, "nope", "203.0.113.60"),
		"csrf":       hn.postForm("/pages", s, url.Values{"csrf": {"-"}}),
	}
	for name, w := range resp {
		if got := w.Header().Get("Vary"); got != vary {
			t.Errorf("%s: Vary = %q, want %q", name, got, vary)
		}
		if got := w.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q", name, got)
		}
	}
}

// --- S3/S4: the selector route -------------------------------------------------------------

func serveLang(hn *harness, rawQuery string, s session) *httptest.ResponseRecorder {
	return hn.serve(s.apply(navigate(httptest.NewRequest(http.MethodGet, "/lang?"+rawQuery, nil))))
}

func TestSelectorSetsCookieAndReturns(t *testing.T) {
	hn := newHarness(t)
	for _, to := range []string{"es", "en"} {
		w := serveLang(hn, "to="+to+"&next=%2Fpages%2Fnew", session{}) // no session at all
		wantStatus(t, w, http.StatusSeeOther)
		if loc := w.Header().Get("Location"); loc != "/pages/new" {
			t.Errorf("Location = %q", loc)
		}
		var c *http.Cookie
		for _, k := range w.Result().Cookies() {
			if k.Name == langCookie {
				c = k
			}
		}
		if c == nil {
			t.Fatalf("no %s cookie: %v", langCookie, w.Result().Cookies())
		}
		if c.Value != to || c.Path != "/" || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode {
			t.Errorf("cookie = %+v", c)
		}
		if year := 365 * 24 * 3600; c.MaxAge < year-7*24*3600 || c.MaxAge > year+7*24*3600 {
			t.Errorf("Max-Age = %d, want about one year", c.MaxAge)
		}
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("security headers missing: %v", w.Header())
		}
	}
	// A query string and a fragment-free path survive the round trip.
	w := serveLang(hn, "to=es&next="+url.QueryEscape("/?lookup=app.test&notice=saved"), session{})
	if loc := w.Header().Get("Location"); loc != "/?lookup=app.test&notice=saved" {
		t.Errorf("Location = %q", loc)
	}
}

func TestSelectorRejectsOpenRedirects(t *testing.T) {
	hn := newHarness(t)
	for name, next := range map[string]string{
		"protocol-relative":      "//evil.test",
		"protocol-relative path": "//evil.test/x",
		"backslash":              `/\evil.test`,
		"backslash inside":       `/ok\evil.test`,
		"absolute https":         "https://evil.test",
		"absolute http":          "http://evil.test/x",
		"javascript":             "javascript:alert(1)",
		"data":                   "data:text/html,x",
		"relative":               "pages/new",
		"empty":                  "",
		"tab trick":              "/\t/evil.test",
		"newline":                "/\r\nSet-Cookie: x=1",
		"double backslash":       `\\evil.test`,
		"too long":               "/" + strings.Repeat("a", 5000),
	} {
		t.Run(name, func(t *testing.T) {
			w := serveLang(hn, "to=es&next="+url.QueryEscape(next), session{})
			wantStatus(t, w, http.StatusSeeOther)
			if loc := w.Header().Get("Location"); loc != "/" {
				t.Errorf("Location = %q, want /", loc)
			}
		})
	}
	t.Run("missing", func(t *testing.T) {
		w := serveLang(hn, "to=es", session{})
		wantStatus(t, w, http.StatusSeeOther)
		if loc := w.Header().Get("Location"); loc != "/" {
			t.Errorf("Location = %q, want /", loc)
		}
	})
}

func TestSelectorIgnoresUnknownLanguages(t *testing.T) {
	hn := newHarness(t)
	for name, q := range map[string]string{
		"fr": "to=fr&next=%2F", "upper": "to=ES&next=%2F", "empty": "to=&next=%2F", "missing": "next=%2F",
		"two": "to=es&to=fr&next=%2F", "long": "to=" + strings.Repeat("e", 4000),
		"injection": "to=es%0d%0aSet-Cookie:x=1&next=%2F", "region": "to=es-ES&next=%2F",
	} {
		t.Run(name, func(t *testing.T) {
			w := serveLang(hn, q, session{})
			wantStatus(t, w, http.StatusSeeOther)
			for _, c := range w.Result().Cookies() {
				if c.Name == langCookie {
					t.Fatalf("an unknown language set a cookie: %+v", c)
				}
			}
			if w.Header().Get("Location") == "" {
				t.Error("no Location")
			}
		})
	}
}

// Only /lang joins /login and /healthz as session-less routes, and only for GET.
func TestSelectorOpensNothingElseWithoutASession(t *testing.T) {
	hn := newHarness(t)
	for _, p := range []string{"/lang/es", "/lang/", "/lang/../pages/new", "/language", "/lang/x?to=es"} {
		w := hn.serve(navigate(httptest.NewRequest(http.MethodGet, p, nil)))
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
			t.Errorf("GET %s without session = %d %q, want the login redirect", p, w.Code, w.Header().Get("Location"))
		}
		for _, c := range w.Result().Cookies() {
			if c.Name == langCookie {
				t.Errorf("GET %s set a language cookie", p)
			}
		}
	}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		w := hn.serve(navigate(httptest.NewRequest(m, "/lang?to=es&next=%2F", nil)))
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /lang = %d, want 405", m, w.Code)
		}
		if w.Header().Get("Referrer-Policy") != "no-referrer" || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s /lang 405 lacks the panel security headers: %v", m, w.Header())
		}
		for _, c := range w.Result().Cookies() {
			if c.Name == langCookie {
				t.Errorf("%s /lang set a language cookie", m)
			}
		}
	}
	// Still gated: the pages and everything else stay behind the session.
	for _, p := range []string{"/", "/pages/new", "/pages/default"} {
		w := hn.serve(navigate(httptest.NewRequest(http.MethodGet, p, nil)))
		wantStatus(t, w, http.StatusSeeOther)
		if got := w.Header().Get("Location"); got != "/login" {
			t.Errorf("GET %s = %q", p, got)
		}
	}
}

func selectorLinks(body string) []string {
	var out []string
	for _, m := range langLinkRe.FindAllStringSubmatch(body, -1) {
		out = append(out, html.UnescapeString(m[1]))
	}
	return out
}

func linkTo(t *testing.T, body, lang string) string {
	t.Helper()
	for _, l := range selectorLinks(body) {
		if strings.HasPrefix(l, "/lang?to="+lang+"&") {
			return l
		}
	}
	t.Fatalf("no selector link to %q in %v\n%s", lang, selectorLinks(body), excerpt(body))
	return ""
}

func TestSelectorIsOnLoginAndNavbarAndWorksWithoutScript(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	hn.createPage("Alpha", "app.test")

	// Login page, signed out: Spanish shown, the link offers English and returns to /login.
	w := hn.serve(withLang(navigate(httptest.NewRequest(http.MethodGet, "/login", nil)), "es"))
	if l := linkTo(t, w.Body.String(), "en"); l != "/lang?to=en&next=%2Flogin" {
		t.Errorf("login selector link = %q", l)
	}
	mustContain(t, w.Body.String(), "ES", "EN")

	// Navbar, signed in: the link returns to the exact page, query included.
	w = hn.getL("/?lookup=app.test", s, "es")
	body := w.Body.String()
	l := linkTo(t, body, "en")
	if want := "/lang?to=en&next=" + url.QueryEscape("/?lookup=app.test"); l != want {
		t.Errorf("navbar selector link = %q, want %q", l, want)
	}
	if !strings.Contains(body[:strings.Index(body, "</header>")], "/lang?") {
		t.Error("the selector is not inside the navbar")
	}
	mustNotContainRaw(t, body, "<script", " onclick=")

	// Follow it with nothing but the cookie jar: English afterwards, same page.
	sw := hn.serve(s.apply(navigate(httptest.NewRequest(http.MethodGet, l, nil))))
	wantStatus(t, sw, http.StatusSeeOther)
	back := sw.Header().Get("Location")
	if back != "/?lookup=app.test" {
		t.Fatalf("returned to %q", back)
	}
	var cookie string
	for _, c := range sw.Result().Cookies() {
		if c.Name == langCookie {
			cookie = c.Value
		}
	}
	after := hn.getL(back, s, cookie)
	if pageLang(after.Body.String()) != "en" {
		t.Error("page is not English after choosing EN")
	}
	mustContain(t, after.Body.String(), "Maintenance pages")
	// ...and back to Spanish through the link the English page offers.
	if l := linkTo(t, after.Body.String(), "es"); !strings.HasPrefix(l, "/lang?to=es&") {
		t.Errorf("link = %q", l)
	}
}

// A page rendered in answer to a POST returns to the GET page of the same thing.
func TestSelectorReturnsFromRenderedPosts(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	a := hn.createPage("Alpha", "app.test")

	w := hn.postMultipartL("/pages", s, templateFields("", "a b"), nil, "es")
	wantStatus(t, w, http.StatusUnprocessableEntity)
	if l := linkTo(t, w.Body.String(), "en"); l != "/lang?to=en&next=%2Fpages%2Fnew" {
		t.Errorf("create errors: %q", l)
	}
	w = hn.postMultipartL("/pages/"+a.ID, s, map[string]string{"name": "", "destinations": "a b"}, nil, "es")
	if l := linkTo(t, w.Body.String(), "en"); l != "/lang?to=en&next="+url.QueryEscape("/pages/"+a.ID) {
		t.Errorf("update errors: %q", l)
	}
	w = hn.postFormL("/pages/"+a.ID+"/delete", s, url.Values{}, "es")
	if l := linkTo(t, w.Body.String(), "en"); l != "/lang?to=en&next="+url.QueryEscape("/pages/"+a.ID) {
		t.Errorf("delete confirm: %q", l)
	}
	w = hn.postLoginL("es", testUser, "nope", "203.0.113.61")
	if l := linkTo(t, w.Body.String(), "en"); l != "/lang?to=en&next=%2Flogin" {
		t.Errorf("login failure: %q", l)
	}
}

// --- S1/S6: Spanish views ------------------------------------------------------------------

func TestSpanishViews(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	const es = "es"

	type view struct {
		w    *httptest.ResponseRecorder
		code int
		want []string
	}
	views := map[string]view{}
	add := func(name string, w *httptest.ResponseRecorder, code int, want ...string) {
		views[name] = view{w, code, want}
	}

	add("login", hn.serve(withLang(navigate(httptest.NewRequest("GET", "/login", nil)), es)), 200,
		"Inicia sesión en backsoon", "Usuario", "Contraseña", "Iniciar sesión", "Saltar al contenido")
	add("login failure", hn.postLoginL(es, testUser, "nope", "203.0.113.50"), 401, "Usuario o contraseña incorrectos.")
	for i := 0; i < 5; i++ {
		hn.postLoginL(es, testUser, "bad", "203.0.113.77")
	}
	add("rate limited", hn.postLoginL(es, testUser, "bad", "203.0.113.77"), 429,
		"Demasiados intentos de inicio de sesión. Inténtalo de nuevo en 15 minutos.")
	add("directory empty", hn.getL("/", s, es), 200,
		"Páginas de mantenimiento", "Crea una página para el dominio de una aplicación", "Crear una página",
		"Añadir página", "Cerrar sesión", "Consultar", "Cómo elige backsoon una página",
		"Actualizada", "Todo lo demás", `aria-label="Acciones de`, "Vista previa a tamaño completo", "Editar")
	add("new", hn.getL("/pages/new", s, es), 200,
		"Nueva página", "Nombre de la página", "Destinos", "Empezar desde", "Nombre de la aplicación", "Mensaje",
		"Idioma", "Icono", "Crear página", "Cancelar", "Volver al directorio", "Un archivo .html subido")

	a := hn.createPage("Alpha", "app.test", "web:8080")
	hn.createPage("Bravo", "bravo.test")
	add("directory", hn.getL("/", s, es), 200, "Alpha", "Bravo", "Destinos", "servicio:puerto: ", "Eliminar…")
	add("directory focus", hn.getL("/?focus="+a.ID+"&notice=created", s, es), 200,
		"Página creada. backsoon la sirve a partir de la próxima solicitud.", "Abrir la vista previa a tamaño completo",
		"Cerrar la vista previa", `title="Vista previa de Alpha"`)
	add("lookup hit", hn.getL("/?lookup=app.test", s, es), 200, "muestra", "Coincidencia exacta de dominio", "Coincide")
	add("lookup service hit", hn.getL("/?lookup=web:8080", s, es), 200, "Coincidencia de servicio:puerto")
	add("lookup miss", hn.getL("/?lookup=zzz.test", s, es), 200, "No hay ninguna página asignada a zzz.test")
	add("lookup miss service", hn.getL("/?lookup=zzz:81", s, es), 200, "A menos que el dominio de la solicitud")
	add("lookup invalid", hn.getL("/?lookup=a%20b", s, es), 200, "no es un destino válido: un dominio solo puede contener letras")
	add("lookup path", hn.getL("/?lookup=http://x.test/p", s, es), 200, "Introduce solo el dominio o el servicio:puerto, sin http:// ni ruta.")
	add("notice saved", hn.getL("/?notice=saved", s, es), 200, "Cambios guardados.")
	add("notice deleted", hn.getL("/?notice=deleted", s, es), 200, "Página eliminada. Sus destinos ahora muestran la página predeterminada.")
	add("notice publish", hn.getL("/?notice=publish-failed", s, es), 200, "Advertencia.", "no pudo publicar los archivos")
	add("edit", hn.getL("/pages/"+a.ID, s, es), 200,
		"Reemplazar el HTML", "Guardar cambios", "Zona de peligro", "Eliminar esta página", "Abrir a tamaño completo", `title="Vista previa de Alpha"`)
	add("edit default", hn.getL("/pages/default", s, es), 200, "Esta página se muestra para", "Todo lo demás", "no se puede eliminar")

	add("new errors", hn.postMultipartL("/pages", s, templateFields("", "a b\napp:99999\nweb:abc"), nil, es), 422,
		"Corrige esto antes de guardar", "El nombre es obligatorio. Solo se muestra en este panel.",
		"el puerto debe estar entre 1 y 65535", "el puerto debe ser un número entre 1 y 65535", "un dominio solo puede contener letras")
	add("edit errors", hn.postMultipartL("/pages/"+a.ID, s, map[string]string{"name": "", "destinations": "a b"}, nil, es), 422,
		"Corrige esto antes de guardar", "Nombre de la página: El nombre es obligatorio")
	add("too large", hn.postMultipartL("/pages", s, map[string]string{"name": "Big", "destinations": "big.test"},
		&upload{name: "big.html", data: []byte("<p>" + strings.Repeat("a", 600*1024))}, es), 413, "El archivo supera los 512 KB.")
	add("not utf8", hn.postMultipartL("/pages", s, map[string]string{"name": "L", "destinations": "l.test"},
		&upload{name: "l.html", data: []byte("\xff")}, es), 422, "El archivo no es UTF-8 válido.", "Archivo HTML")
	add("empty file", hn.postMultipartL("/pages", s, map[string]string{"name": "L", "destinations": "l.test", "source": "upload"},
		&upload{name: "l.html", data: []byte{}}, es), 422, "El archivo está vacío. Elige un archivo .html que tenga contenido.")
	add("no file chosen", hn.postMultipartL("/pages", s, map[string]string{"name": "L", "destinations": "l.test", "source": "upload"}, nil, es), 422,
		"Elige un archivo .html para subir.")
	add("wrong ext", hn.postMultipartL("/pages", s, map[string]string{"name": "L", "destinations": "l.test", "source": "upload"},
		&upload{name: "l.txt", data: []byte("<p>x")}, es), 422, "Solo se pueden subir archivos .html.")
	add("no destinations", hn.postMultipartL("/pages", s, templateFields("Acme", "  "), nil, es), 422,
		"Añade al menos un destino, como app.example.com o app:8080.")
	add("warning confirm", hn.postMultipartL("/pages", s, map[string]string{"name": "W", "destinations": "w.test", "source": "upload"},
		&upload{name: "w.html", data: []byte(`<link href="https://x/a.css"><script>1</script>`)}, es), 200,
		"Revisa esta página antes de publicarla", "está lista para crearse", "Publicar de todos modos", "Volver y editar", "Advertencia.",
		"La página hace referencia a recursos externos (&lt;link&gt;)", "La página contiene código ejecutable (&lt;script&gt;); backsoon lo bloquea, así que no se ejecutará para los visitantes")
	add("conflict confirm", hn.postMultipartL("/pages", s, templateFields("Charlie", "bravo.test, app.test"), nil, es), 409,
		"¿Mover destinos?", "Mover destinos", "Ahora los muestra", "se quedará sin destinos")
	add("delete confirm", hn.postFormL("/pages/"+a.ID+"/delete", s, url.Values{}, es), 200,
		"¿Eliminar “Alpha”?", "Estos destinos mostrarán la página predeterminada", "No se puede deshacer.", "Conservar página", "Eliminar página")
	add("default delete", hn.postFormL("/pages/default/delete", s, url.Values{}, es), 409,
		"La página predeterminada no se puede eliminar", "Volver a la página")
	add("404", hn.getL("/pages/none", s, es), 404, "Página no encontrada", "No hay nada en esta dirección.", "Volver al directorio")
	add("csrf", hn.postFormL("/pages", s, url.Values{"csrf": {"-"}}, es), 403, "Este formulario ha caducado")
	add("draft gone", hn.postFormL("/pages", s, url.Values{"draft": {"missing"}, "confirm": {"warnings"}}, es), 410,
		"La subida pendiente caducó")

	for name, v := range views {
		t.Run(name, func(t *testing.T) {
			if v.w.Code != v.code {
				t.Fatalf("status = %d, want %d\n%s", v.w.Code, v.code, excerpt(v.w.Body.String()))
			}
			mustBeSpanish(t, name, v.w, v.want...)
			// Structurally the same page as the English one: markup checks still hold.
			checkWellFormed(t, name, v.w.Body.String())
			if n := strings.Count(styleRe.ReplaceAllString(v.w.Body.String(), ""), "<h1"); n != 1 {
				t.Errorf("%d h1 elements", n)
			}
		})
	}
}

func TestSpanishDefaultPageHasNoDestinationsError(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	w := hn.postMultipartL("/pages/default", s, map[string]string{"name": "Todo", "destinations": "x.test"}, nil, "es")
	wantStatus(t, w, http.StatusConflict)
	mustBeSpanish(t, "default dests", w, "La página predeterminada no tiene destinos", "Volver a la página")
}

func TestSpanishTemplateFieldErrors(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	long := strings.Repeat("x", 501)
	for name, tc := range map[string]struct {
		mod  func(map[string]string)
		want string
	}{
		"app name required": {func(f map[string]string) { f["app_name"] = " " }, "El nombre de la aplicación es obligatorio"},
		"app name long":     {func(f map[string]string) { f["app_name"] = strings.Repeat("x", 101) }, "El nombre de la aplicación debe ser texto válido de 100 caracteres como máximo"},
		"app name control":  {func(f map[string]string) { f["app_name"] = "a\x01b" }, "El nombre de la aplicación contiene caracteres de control"},
		"message required":  {func(f map[string]string) { f["message"] = "" }, "El mensaje es obligatorio"},
		"message long":      {func(f map[string]string) { f["message"] = long }, "El mensaje debe ser texto válido de 500 caracteres como máximo"},
		"message control":   {func(f map[string]string) { f["message"] = "a\x01b" }, "El mensaje contiene caracteres de control"},
		"icon long":         {func(f map[string]string) { f["icon"] = strings.Repeat("x", 17) }, "El icono debe ser texto válido de 16 caracteres como máximo"},
		"icon control":      {func(f map[string]string) { f["icon"] = "a\x01" }, "El icono contiene caracteres de control"},
		"bad language":      {func(f map[string]string) { f["lang"] = "not a tag" }, "El idioma debe ser una etiqueta como"},
	} {
		t.Run(name, func(t *testing.T) {
			f := templateFields("Acme", "app.test")
			tc.mod(f)
			w := hn.postMultipartL("/pages", s, f, nil, "es")
			wantStatus(t, w, http.StatusUnprocessableEntity)
			mustBeSpanish(t, name, w, tc.want)
		})
	}
	t.Run("name too long and control", func(t *testing.T) {
		page := &upload{name: "p.html", data: []byte(validPage)}
		for name, tc := range map[string]struct{ value, want string }{
			"long":    {strings.Repeat("n", 101), "Usa texto válido de 100 caracteres como máximo"},
			"control": {"a\x01b", "El nombre contiene caracteres de control"},
		} {
			f := map[string]string{"name": tc.value, "destinations": "app.test", "source": "upload"}
			w := hn.postMultipartL("/pages", s, f, page, "es")
			wantStatus(t, w, http.StatusUnprocessableEntity)
			mustBeSpanish(t, "name "+name, w, tc.want)
		}
	})
	t.Run("too many destinations", func(t *testing.T) {
		var dests []string
		for i := 0; i < 101; i++ {
			dests = append(dests, "d"+strings.Repeat("x", i%5)+string(rune('a'+i%26))+".test")
		}
		w := hn.postMultipartL("/pages", s, templateFields("Acme", strings.Join(dests, "\n")), nil, "es")
		wantStatus(t, w, http.StatusUnprocessableEntity)
		mustBeSpanish(t, "too many", w, "Demasiados destinos (máximo 100 por página).")
	})
	t.Run("edit needs a destination", func(t *testing.T) {
		a := hn.createPage("Alpha", "app.test")
		w := hn.postMultipartL("/pages/"+a.ID, s, map[string]string{"name": "Alpha", "destinations": " "}, nil, "es")
		wantStatus(t, w, http.StatusUnprocessableEntity)
		mustBeSpanish(t, "edit no dests", w, "Añade al menos un destino o elimina la página.")
	})
}

func TestSpanishPlainAndFrameNotices(t *testing.T) {
	hn := newHarness(t)
	site := hn.createPage("Alpha", "app.test")

	r := fetchReq(httptest.NewRequest(http.MethodGet, "/", nil))
	r.Header.Set("Accept-Language", "es-ES,es;q=0.9")
	w := hn.serve(r)
	wantStatus(t, w, http.StatusUnauthorized)
	if got := w.Body.String(); got != "Autenticación requerida. Inicia sesión en /login.\n" {
		t.Errorf("401 body = %q", got)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	// English stays exactly as it was.
	w = hn.serve(fetchReq(httptest.NewRequest(http.MethodGet, "/", nil)))
	if got := w.Body.String(); got != "Authentication required. Sign in at /login.\n" {
		t.Errorf("English 401 body = %q", got)
	}

	fr := navigate(httptest.NewRequest(http.MethodGet, "/pages/"+site.ID+"/preview", nil))
	fr.Header.Set("Accept-Language", "es")
	w = hn.serve(fr)
	wantStatus(t, w, http.StatusUnauthorized)
	body := w.Body.String()
	mustContain(t, body, `lang="es"`, "Los enlaces dentro de la vista previa no funcionan", "Cierra y vuelve a abrir la vista previa")
	mustNotContain(t, body, "Links inside", "app.test", site.ID)
	if w.Header().Get("Content-Security-Policy") != frameNoticeCSP {
		t.Error("frame notice CSP changed")
	}
}

// --- S1/S2: English stays the default and is never mixed in -------------------------------

func TestCookieBeatsAcceptLanguageOnEveryView(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	a := hn.createPage("Alpha", "app.test")
	for _, p := range []string{"/", "/pages/new", "/pages/" + a.ID, "/pages/none"} {
		r := s.apply(navigate(httptest.NewRequest(http.MethodGet, p, nil)))
		r.Header.Set("Accept-Language", "es")
		w := hn.serve(withLang(r, "en"))
		if got := pageLang(w.Body.String()); got != "en" {
			t.Errorf("%s: lang = %q with an en cookie", p, got)
		}
		mustNotContain(t, w.Body.String(), "Cerrar sesión")
		mustContain(t, w.Body.String(), "Sign out")
	}
}

// Logs are for operators: they stay English whatever the browser language (RNF-9).
func TestLogsStayEnglish(t *testing.T) {
	hn := newHarnessWith(t, io.ErrClosedPipe)
	hn.postLoginL("es", testUser, "nope", "203.0.113.70")
	s := hn.login()
	hn.postMultipartL("/pages", s, templateFields("Acme", "app.test"), nil, "es")
	logs := hn.logs.String()
	for _, want := range []string{"login failed ip=203.0.113.70", "publish failed", "login ok user="} {
		if !strings.Contains(logs, want) {
			t.Errorf("log lacks %q:\n%s", want, logs)
		}
	}
	for _, bad := range []string{"incorrect", "Inicia", "fallo", "página", "Página"} {
		if strings.Contains(logs, bad) {
			t.Errorf("log contains Spanish %q:\n%s", bad, logs)
		}
	}
}

// The seeded default page is stored as "Default page"; while it keeps that name the panel shows it
// in the language of the response. The stored value (and the edit field) stay as they are.
func TestSeededDefaultPageNameIsLocalized(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	hn.createPage("Alpha", "app.test")
	const es = "Página predeterminada"

	for name, w := range map[string]*httptest.ResponseRecorder{
		"directory":     hn.getL("/", s, "es"),
		"lookup miss":   hn.getL("/?lookup=zzz.test", s, "es"),
		"lookup focus":  hn.getL("/?focus=default&lookup=zzz:81", s, "es"),
		"edit default":  hn.getL("/pages/default", s, "es"),
		"delete others": hn.postFormL("/pages/"+hn.store.List()[1].ID+"/delete", s, url.Values{}, "es"),
	} {
		body := w.Body.String()
		mustContain(t, body, es)
		textOnly := textareaRe.ReplaceAllString(styleRe.ReplaceAllString(body, ""), "")
		textOnly = regexp.MustCompile(`value="[^"]*"`).ReplaceAllString(textOnly, "")
		if strings.Contains(textOnly, "Default page") {
			t.Errorf("%s: the seeded name shows untranslated\n%s", name, excerpt(body))
		}
	}
	w := hn.getL("/pages/default", s, "es")
	mustContain(t, w.Body.String(), `value="Default page"`, "<title>Editar "+es+" · backsoon</title>", `title="Vista previa de `+es+`"`)

	// English stays exactly as it was.
	mustContain(t, hn.getL("/", s, "en").Body.String(), "Default page")
	mustNotContain(t, hn.getL("/", s, "en").Body.String(), es)

	// Confirming content warnings on the default page names it too.
	w = hn.postMultipartL("/pages/default", s, map[string]string{"name": "Default page"},
		&upload{name: "w.html", data: []byte(`<script>1</script>`)}, "es")
	wantStatus(t, w, http.StatusOK)
	mustContain(t, w.Body.String(), "“"+es+"” recibirá HTML nuevo")

	// A renamed default page shows its own name in every language, and the lookup answer too.
	if _, err := hn.store.Update("default", pages.Update{Name: ptr("Mantenimiento general")}, false); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"es", "en"} {
		body := hn.getL("/?lookup=zzz.test", s, lang).Body.String()
		mustContain(t, body, "Mantenimiento general")
		mustNotContain(t, body, es, "Default page")
	}
}

// Only the default page gets this treatment: a page the user names "Default page" is user data.
func TestUserPageCalledDefaultPageStaysAsTyped(t *testing.T) {
	hn := newHarness(t)
	s := hn.login()
	hn.createPage("Default page", "other.test")
	body := hn.getL("/", s, "es").Body.String()
	if n := strings.Count(body, `class="name" href="/pages/default"`); n != 1 {
		t.Fatalf("default page rows = %d", n)
	}
	if n := strings.Count(textareaRe.ReplaceAllString(styleRe.ReplaceAllString(body, ""), ""), ">Default page<"); n != 1 {
		t.Errorf("the user's own page called \"Default page\" appears %d times, want 1", n)
	}
}

func ptr(s string) *string { return &s }
