package render

import (
	"go-htmx-starter/internal/auth"
	"html/template"
	"log/slog"
	"net/http"
	"path/filepath"
)

// funcMap holds template helper functions.
// Add your own here as the app grows.
var funcMap = template.FuncMap{}

func authState(r *http.Request) (bool, string) {
	userID := auth.GetUserID(r)
	email := auth.GetEmail(r)
	return userID != "", email
}

// mergeData combines caller-supplied data with global auth state.
// Caller data always wins on key conflicts (except auth keys which we own).
func mergeData(r *http.Request, data interface{}) map[string]interface{} {
	out := map[string]interface{}{}

	// Copy caller data first
	if m, ok := data.(map[string]interface{}); ok {
		for k, v := range m {
			out[k] = v
		}
	}

	// Inject auth state — always overwrite so handlers can't accidentally lie
	authenticated, email := authState(r)
	out["Authenticated"] = authenticated
	out["Email"] = email

	return out
}

// Fragment renders a named block from a template file (used for HTMX partial swaps).
func Fragment(w http.ResponseWriter, filename string, block string, data interface{}) {
	tmpl, err := template.New("").Funcs(funcMap).ParseFiles(filepath.Join("web", "templates", filename))
	if err != nil {
		slog.Error("render: failed to parse template", "file", filename, "err", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if err := tmpl.ExecuteTemplate(w, block, data); err != nil {
		slog.Error("render: failed to execute template", "file", filename, "block", block, "err", err)
	}
}

// Template renders a full page or HTMX content block, automatically injecting auth state.
func Template(w http.ResponseWriter, r *http.Request, page string, data interface{}) {
	files := []string{
		filepath.Join("web", "templates", "layout.html"),
		filepath.Join("web", "templates", page),
	}

	tmpl, err := template.New("").Funcs(funcMap).ParseFiles(files...)
	if err != nil {
		slog.Error("render: failed to parse template", "file", page, "err", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	merged := mergeData(r, data)

	target := "base"
	if r.Header.Get("HX-Request") == "true" {
		target = "content"
	}

	if err = tmpl.ExecuteTemplate(w, target, merged); err != nil {
		slog.Error("render: failed to execute template", "file", page, "block", target, "err", err)
	}
}
