package bundle

import (
	"bytes"
	"encoding/base64"
	"html/template"
	"strings"

	"github.com/openclaw/beam/internal/packet"
)

// Only verified media types and base64 bytes become URLs. All sender-controlled
// text, including filenames and the transcript, stays in escaped text contexts.
var preview = template.Must(template.New("preview").Funcs(template.FuncMap{
	"kind": func(mime string) string { kind, _, _ := strings.Cut(mime, "/"); return kind },
	"speaker": func(kind string) string {
		switch kind {
		case "userMessage":
			return "User"
		case "agentMessage":
			return "Agent"
		default:
			return "Other"
		}
	},
}).Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; media-src data:; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'">
<title>{{.Packet.Title}} · Beam</title><style>
*{box-sizing:border-box}body{margin:0 auto;padding:2rem 1rem;max-width:52rem;background:#f8f6ef;color:#22291f;font:1rem/1.55 system-ui,sans-serif}h1,h2,h3,p{overflow-wrap:anywhere}header,article,figure{margin:0 0 1rem;padding:1rem;border-bottom:1px solid #cdcec2}h1,h2,h3{line-height:1.15}header p,figcaption p{color:#515748}figure{background:white;border-radius:.4rem}img,video,audio{max-width:100%;max-height:65vh}a{color:#8b351c}article p{white-space:pre-wrap}figcaption{margin-bottom:.75rem}
</style></head><body><header><p>Beam conversation</p><h1>{{.Packet.Title}}</h1><p>{{.Packet.Source}} · {{.Packet.UpdatedAt}}{{if .Packet.Truncated}} · Partial history{{end}}</p></header>
<main><section><h2>Attachments</h2>{{range .Assets}}<figure><figcaption><strong>{{.Name}}</strong><p>{{.MediaType}} · {{.Bytes}} bytes</p></figcaption>
{{if eq (kind .MediaType) "image"}}<img src="{{.URL}}" alt="{{.Name}}">
{{else if eq (kind .MediaType) "audio"}}<audio controls preload="none" src="{{.URL}}"></audio>
{{else if eq (kind .MediaType) "video"}}<video controls preload="none" src="{{.URL}}"></video>
{{else}}<a download="{{.Download}}" href="{{.URL}}">Save {{.Name}}</a>{{end}}</figure>{{end}}</section>
<section><h2>Conversation</h2>{{with .Packet.Goal}}<article><h3>Historical goal (reference only)</h3><p>{{.ReferenceText}}</p></article>{{end}}{{range .Packet.Items}}<article><h3>{{speaker .Type}}{{if .Speaker}} · {{.Speaker}}{{end}}</h3><p>{{.Text}}</p></article>{{end}}</section></main></body></html>
`))

func (b *Bundle) Preview() ([]byte, error) {
	type attachment struct {
		Asset
		URL      template.URL
		Download string
	}
	page := struct {
		Packet packet.Packet
		Assets []attachment
	}{Packet: b.Packet}
	for i, a := range b.Manifest.Assets {
		src := "data:" + a.MediaType + ";base64," + base64.StdEncoding.EncodeToString(b.files[i])
		page.Assets = append(page.Assets, attachment{a, template.URL(src), strings.TrimPrefix(a.Path, "assets/")})
	}
	var out bytes.Buffer
	err := preview.Execute(&out, page)
	return out.Bytes(), err
}
