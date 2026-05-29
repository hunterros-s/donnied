package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// dist is produced by `npm run build` in ./frontend.
//
//go:embed all:dist
var dist embed.FS

// Handler serves the built Preact app. Unknown non-file GET paths fall back to
// index.html so the frontend can own client-side routes later.
func Handler() http.Handler {
	appFS, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(appFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" && fileExists(appFS, path) {
			files.ServeHTTP(w, r)
			return
		}

		clone := r.Clone(r.Context())
		clone.URL.Path = "/"
		files.ServeHTTP(w, clone)
	})
}

func fileExists(fsys fs.FS, name string) bool {
	file, err := fsys.Open(name)
	if err != nil {
		return false
	}
	defer file.Close()
	info, err := file.Stat()
	return err == nil && !info.IsDir()
}
