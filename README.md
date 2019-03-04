Logster - A simple log shipper for multiple files
=================================================

**STATUS**: Development started, no functional release yet

Abstract
--------

Logster is a simple tool to forward logs to another server running a compatible server.

Logster is composed of a client and a server daemon using a simple command
protocol to optimize log transfer and guarantee delivery.

Logster client and server is written in [Go](http://golang.org) and
is tested for Linux only for now.

Client logsterc
---------------

The logster client allows watching multiple files for changes and sending their
contents to a preconfigured server.

### Configuration

Configuration is handled in a TOML file.
The logster client may either be started with a configuration file or
get a list of files to stream on the commandline.

The configuration file will allow more flexible configuration for the
files to stream.

    [server]
    hostname=log.mgmt.neotel.at
    port=8901
    compression=none     # snappy, gzip, lz4

    [stream:messages]
    path=/var/log/messages
    method=watch
    onrotate=follow

    [stream:backup]
    directory=/var/log/sipproxyd/backup
    method=watch
    filepattern='*.log.*.gz'

### Log streaming

Logfiles can be streamed to the server. This requires logster to
follow the current position in the files and send new content as
soon as possible to the server.

### File sending

For file sending only existing files are copied to the server

Server logsterd
---------------

The logster daemon (or logsterd) is a simple daemon accepting mulitple
incoming connections on a single port.

The server will be configured to match incoming meta data like hostname,
filename or other meta data to an outgoing file.

An incoming log streaming request will then be matched to an output
logfile configuration.

    [log.live2.coolapp]
    path=/var/log/logster/live/coolapp.log
    maxsize=10M
    rotate=10

    [log.live2.niceapp]
    path=/var/log/logster/live2/sipproxyd.log
    maxsize=10M
    rotate=10

Log Protocol
------------

The log protocol is a very minimal initial handshape between client
and server to be able to provide metadata.

The protocol is intentionally not based on HTTP to avoid unneeded
overhead for this simple use case.

* Client initiate TCP connection
* Server accepts connection, sends HELLO
* Client inits stream
* Server acknowledges stream
* Client streams logfile
* Client or Server close the TCP connection

### Stream Initialization

```text
>>  TCP Connect
 << ### Welcome to Logster server v0.1
 << STREAMID abcdef
>>  INIT STREAM host:/path svc:servicename more:<meta>
 << 200 OK Ready to accept data
```

In case of error an appropriate error text is shown and the
command may be retried after a few seconds.

### Streaming Data

Data is now sent directly over TCP (no overhead, only TCP headers).

In case any unrecoverable failure occures, the underlying TCP connection
of the stream must simply be close/disconnected.

If further data shall be sent, the stream must then be reconnected and
initialized again.

```text
>>  DATA.DATA.DATA.DATA.DATA.DATA...
>>  close TCP connection
 << or server closes TCP connection
```

## Rate Limiting

Rate limiting can be achieved, by throtteling the amount of data sent per
time. *Currently not implemented.*

Handling
--------

A client controls all streams and can issue a streamFile.

    client := logster.NewClient("log.example.com:8901")
    n, err := client.StreamFile("/var/log/app.log")
    n, err := client.StreamFile("/var/log/messages")

The logster client will follow file changes automatically and 
reconnect to the server.

Log sending mechanisms
----------------------

### Send after close

For the send after close mechanism, the file is sent to the server only after
the file is closed and rotated.

The rotated file will be streamed to the server verbatim with the optimal
perfomance (rate limiting will be considered when implemented).

This sending mechanism will provide optimal performance and allows for very
simple housekeeping by also removing the file after being sent.

### Stream

The file is watched for changes and will be streamed immediatelly. To improve
performance changes will be sent when minimum size (default 4Kb) is reached.

The last sent position will be tracked and written to a state file. The state
file holds the state for all streams being processed. There is a minimal chance
of logs being sent twice in case of a crashing before writing the state.

The file may also be automatically deleted after the file has been closed.
This also requires a watch on the file to react on the file close event.

Building
--------

To create static builds for the binaries use:

    CGO_ENABLED=0 go build -ldflags '-s -w' -o logsterc cmd/logsterc/main.go
    CGO_ENABLED=0 go build -ldflags '-s -w' -o logsterd cmd/logsterd/main.go
