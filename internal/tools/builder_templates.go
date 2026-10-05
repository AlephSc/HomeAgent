// Package tools — template scaffold F10 builder.
package tools

import "fmt"

func staticHTML(name, desc string) string {
	if desc == "" {
		desc = "Dibuat oleh aleph-agent"
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="id">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>%s</title>
<link rel="stylesheet" href="style.css">
</head>
<body>
<h1>%s</h1>
<p>%s</p>
<div id="app">Memuat…</div>
<script src="app.js"></script>
</body>
</html>
`, name, name, desc)
}

func flaskApp(name, desc string) string {
	if desc == "" {
		desc = "Dibuat oleh aleph-agent"
	}
	return fmt.Sprintf(`"""%s — %s"""
import os
from flask import Flask, jsonify

app = Flask(__name__)

@app.route("/")
def index():
    return "<h1>%s</h1><p>%s</p><p>API: <code>/api/status</code></p>"

@app.route("/api/status")
def status():
    return jsonify(app="%s", status="ok", port=int(os.environ.get("PORT", 5000)))

if __name__ == "__main__":
    app.run(host="0.0.0.0", port=int(os.environ.get("PORT", 5000)))
`, name, desc, name, desc, name)
}

func goApp(name, desc string) string {
	if desc == "" {
		desc = "Dibuat oleh aleph-agent"
	}
	return fmt.Sprintf(`// %s — %s
package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8100"
	}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "<h1>%s</h1><p>%s</p>")
	})
	http.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `+"`"+`{"app":"%s","status":"ok"}`+"`"+`)
	})
	fmt.Println("listening on :" + port)
	http.ListenAndServe(":"+port, nil)
}
`, name, desc, name, desc, name)
}

func nodeApp(name, desc string) string {
	if desc == "" {
		desc = "Dibuat oleh aleph-agent"
	}
	return fmt.Sprintf(`// %s — %s
const http = require('http');
const port = process.env.PORT || 8100;

http.createServer((req, res) => {
  if (req.url === '/api/status') {
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({ app: '%s', status: 'ok' }));
    return;
  }
  res.setHeader('Content-Type', 'text/html; charset=utf-8');
  res.end('<h1>%s</h1><p>%s</p>');
}).listen(port, () => console.log('listening on ' + port));
`, name, desc, name, name, desc)
}
