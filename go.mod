module github.com/ftl/tciadapter

go 1.15

//replace github.com/ftl/tci => ../tci

//replace github.com/ftl/rigproxy => ../rigproxy

require (
	github.com/ftl/rigproxy v0.0.0-20210129152621-d47864ba93b5
	github.com/ftl/tci v0.0.0-20210130141754-2ffe755d9f40
	github.com/spf13/cobra v1.1.1
	golang.org/x/sys v0.0.0-20210630005230-0f9fa26af87c
	gopkg.in/check.v1 v1.0.0-20190902080502-41f04d3bba15 // indirect
)
