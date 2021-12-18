module github.com/ftl/tciadapter

go 1.17

//replace github.com/ftl/tci => ../tci

//replace github.com/ftl/rigproxy => ../rigproxy

require (
	github.com/ftl/rigproxy v0.0.0-20210129152621-d47864ba93b5
	github.com/ftl/tci v0.1.3
	github.com/spf13/cobra v1.1.1
	golang.org/x/sys v0.0.0-20210630005230-0f9fa26af87c
)

require (
	github.com/ftl/hamradio v0.0.0-20200721200456-334cc249f095 // indirect
	github.com/gorilla/websocket v1.4.2 // indirect
	github.com/inconshreveable/mousetrap v1.0.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
)
