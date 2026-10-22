module github.com/AllIsA1/lucx-plugins

go 1.26.0

// The overlay holds files of another Go module (the panel); they must not be built here.
ignore (
	./overlay
	./work
)

require golang.org/x/sys v0.49.0 // indirect
