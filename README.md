# TCI-Hamlib Adapter

The TCI-Hamlib Adapter allows to use the [Hamlib](https://github.com/Hamlib/Hamlib) network protocol to communicate with SDRs that only support Expert Electornic's [TCI protocol](https://github.com/maksimus1210/TCI).

Currently the adapter works with the following applications:

* [CQRLog](https://www.cqrlog.com/)
* [FLDigi](http://www.w1hkj.com/)
* [WSJT-X](https://www.physics.princeton.edu/pulsar/k1jt/wsjtx.html)

I develop and test the TCI-Hamlib Adapter on a Linux PC, but I also provide binaries for Raspberry Pi and Windows.

## Usage

The TCI-Hamlib Adapter is a command-line application. It has the following parameters:

```
  -h, --help                   help for tciadapter
  -l, --local_address string   Use this local address to listen for incoming Hamlib connections (default "localhost:4532")
  -d, --no_digimodes           Use LSB/USB instead of the digital modes DIGL/DIGU
  -t, --tci_host string        Connect the adapter to this TCI host (default "localhost:40001")
  -x, --trx int                Use this TRX of the TCI host
```

When there are no parameters given, the adapter uses both for Hamlib and TCI the default ports. If all your applications run on the same machine, using the default ports, this is the way to go:

    tciadapter


## Build

This tool is written in [Go](https://golang.org), so you need the latest Go on your computer in order to build it. As it does not have any other fancy dependencies, it can be build with a simple:

```
go build
```

## Install on Debian-based Linux

* Download the latest .deb package from [Releases](https://github.com/ftl/tciadapter/releases/latest),
* Install the package using `sudo apt install ./tciadapter_<version>_<arch>.deb` (of course, use the name of the file that you downloaded...).

For more information about how to use the CLI client application, simply run the command `tciadapter --help`. 

The deb package also installs a systemd unit that runs the tciadapter as service. This unit is disabled by default. To run tciadapter automatically as service:

* Edit `/etc/systemd/system/tciadapter.service` to your needs,
* `sudo systemctl daemon-reload`
* `sudo systemctl enable tciadapter.service`
* `sudo systemctl start tciadapter.service`

## License
This software is published under the [MIT License](https://www.tldrlegal.com/l/mit).

Copyright [Florian Thienel](http://thecodingflow.com/)
