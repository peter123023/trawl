package web

import (
	"embed"
	"io/fs"
	"net/http"
)

// dist is built by `cd web && npm run build` and committed, so a plain
// `go build` still yields one working binary.
//
//go:embed all:dist
var dist embed.FS

const notBuilt = `<!doctype html><html><body style="font-family:Georgia,serif;padding:48px;color:#14130f;background:#faf8f4">
<h2>Trawl UI 未构建</h2><p>在仓库根目录执行：</p><pre>cd web &amp;&amp; npm install &amp;&amp; npm run build</pre>
</body></html>`

// Index serves the SPA shell for /, /t/{id}, /queue and /queue/{id}; the client
// routes on location.pathname.
func Index() []byte {
	body, err := fs.ReadFile(dist, "dist/index.html")
	if err != nil {
		return []byte(notBuilt)
	}
	return body
}

// Assets serves the hashed bundles emitted by Vite under /assets/.
func Assets() http.Handler {
	sub, err := fs.Sub(dist, "dist/assets")
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.StripPrefix("/assets/", http.FileServer(http.FS(sub)))
}
