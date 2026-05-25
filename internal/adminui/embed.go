package adminui

import (
	"embed"
	"io/fs"
)

//go:embed static/*
var embeddedStatic embed.FS

var staticFS = mustStaticFS()

func mustStaticFS() fs.FS {
	static, err := fs.Sub(embeddedStatic, "static")
	if err != nil {
		panic(err)
	}
	return static
}
