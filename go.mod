module github.com/ftl/tciadapter

go 1.17

//replace github.com/ftl/tci => ../tci

// replace github.com/ftl/rigproxy => ../rigproxy

require (
	github.com/ftl/rigproxy v0.0.0-20211222110853-35af91f708ae
	github.com/ftl/tci v0.2.0
	github.com/spf13/cobra v1.3.0
	golang.org/x/sys v0.0.0-20211216021012-1d35b9e2eb4e
)

require (
	github.com/ftl/hamradio v0.0.0-20210620180211-c5cf51256994 // indirect
	github.com/gorilla/websocket v1.4.2 // indirect
	github.com/inconshreveable/mousetrap v1.0.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
)
