module github.com/ftl/tciadapter

go 1.15

//replace github.com/ftl/tci => ../tci

//replace github.com/ftl/rigproxy => ../rigproxy

require (
	github.com/ftl/rigproxy v0.0.0-20210129152621-d47864ba93b5
	github.com/ftl/tci v0.0.0-20210130141754-2ffe755d9f40
	github.com/spf13/cobra v1.1.1
)
