package openapi

import (
	"bytes"
	"encoding/json"
	"html/template"
	"sort"
)

const docsTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8" />
<title>{{.Title}}</title>
<link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui.css" />
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui-bundle.js" crossorigin></script>
<script>
window.onload = function () {
  window.ui = SwaggerUIBundle({ url: '{{.SpecURL}}', dom_id: '#swagger-ui', deepLinking: true });
};
</script>
</body>
</html>
`

type Operation struct {
	Summary     string             `json:"summary,omitempty"`
	Description string             `json:"description,omitempty"`
	Tags        []string           `json:"tags,omitempty"`
	Security    []map[string][]any `json:"security,omitempty"`
	Parameters  []map[string]any   `json:"parameters,omitempty"`
	RequestBody map[string]any     `json:"requestBody,omitempty"`
	Responses   map[string]any     `json:"responses,omitempty"`
	Extra       map[string]any     `json:"-"`
	raw         map[string]any     `json:"-"`
}

type Document struct {
	Title       string
	Version     string
	Description string
	paths       map[string]map[string]any
}

func New(title, version, description string) *Document {
	return &Document{
		Title:       title,
		Version:     version,
		Description: description,
		paths:       map[string]map[string]any{},
	}
}

func (d *Document) AddPath(path, method string, op Operation) {
	if d.paths == nil {
		d.paths = map[string]map[string]any{}
	}
	payload := map[string]any{}
	if op.Summary != "" {
		payload["summary"] = op.Summary
	}
	if op.Description != "" {
		payload["description"] = op.Description
	}
	if len(op.Tags) > 0 {
		payload["tags"] = op.Tags
	}
	if op.Security != nil {
		payload["security"] = op.Security
	}
	if len(op.Parameters) > 0 {
		payload["parameters"] = op.Parameters
	}
	if op.RequestBody != nil {
		payload["requestBody"] = op.RequestBody
	}
	if len(op.Responses) > 0 {
		payload["responses"] = op.Responses
	}
	for key, value := range op.raw {
		payload[key] = value
	}
	if d.paths[path] == nil {
		d.paths[path] = map[string]any{}
	}
	d.paths[path][method] = payload
}

func (d *Document) Paths() []string {
	out := make([]string, 0, len(d.paths))
	for path := range d.paths {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func (d *Document) JSON() []byte {
	doc := map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       d.Title,
			"version":     d.Version,
			"description": d.Description,
		},
		"servers": []any{map[string]any{"url": "/api/v1"}},
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"access-token": map[string]any{
					"type":         "http",
					"scheme":       "bearer",
					"bearerFormat": "JWT",
				},
			},
		},
		"paths": d.paths,
	}
	payload, err := json.Marshal(doc)
	if err != nil {
		return []byte(`{"openapi":"3.0.3","paths":{}}`)
	}
	return payload
}

func (d *Document) HTML(specURL string) ([]byte, error) {
	tmpl, err := template.New("docs").Parse(docsTemplate)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]string{"Title": d.Title, "SpecURL": specURL}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
