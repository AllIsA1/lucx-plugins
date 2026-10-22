module github.com/AllIsA1/lucx-plugins

go 1.25

// The overlay holds files of another Go module (the panel); they must not be built here.
ignore (
	./overlay
	./work
)
