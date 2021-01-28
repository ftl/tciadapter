# TCI-Hamlib Adapter

The TCI-Hamlib Adapter allows to use the [Hamlib](https://github.com/Hamlib/Hamlib) network protocol to communicate with SDRs that only support Expert Electornic's [TCI protocol](https://github.com/maksimus1210/TCI).

## Build

This tool does not have any fancy dependencies, so it can be build with a simple:

```
go build
```

## Install

To install the CLI client application, simply use the `go install` command:

```
go install github.com/ftl/tciadapter
```

For more information about how to use the CLI client application, simply run the command `tciadapter --help`.

## License
This software is published under the [MIT License](https://www.tldrlegal.com/l/mit).

Copyright [Florian Thienel](http://thecodingflow.com/)
