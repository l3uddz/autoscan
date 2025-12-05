package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:ui/dist
var uiFS embed.FS

// UIHandler returns an http.Handler that serves the embedded UI files
// with SPA fallback support (serves index.html for non-file routes)
func UIHandler() http.Handler {
	// Strip the "ui/dist" prefix to serve from root
	subFS, err := fs.Sub(uiFS, "ui/dist")
	if err != nil {
		panic(err)
	}

	fileServer := http.FileServer(http.FS(subFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Clean the path
		upath := path.Clean(r.URL.Path)
		if upath == "" || upath == "." {
			upath = "/"
		}

		// Remove leading slash for fs.Open
		fsPath := strings.TrimPrefix(upath, "/")
		if fsPath == "" {
			fsPath = "index.html"
		}

		// Check if file exists
		f, err := subFS.Open(fsPath)
		if err != nil {
			// File not found - serve index.html for SPA routing
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}
		f.Close()

		// Serve the file
		r.URL.Path = upath
		fileServer.ServeHTTP(w, r)
	})
}
