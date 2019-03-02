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

    [send:]
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

The logster daemon (or logsterd) is a simple daemon accepting incoming
connections on multiple interfaces.

Predfined tags can be configured on an interface.

    /var/log/logster/<tag>/<logname>.log

Log Protocol
------------

The log protocol allows for a simple handshake between server and client and
is a pure text based protocol.

A protocol command can be identified as `\n%%`. Clients are requested
to escape such a sequence using `\n\%%`.

For a new logstream to be initialized some meta data will be exchanged
and a unique streaming id will be generated. 

Known protocol actions:

* Initiate stream
* Send to stream
* Close stream

The corresponding server commands would look like this:

    %%INIT;<hostname>;<logfile>
    %%SEND <streamid>;<size>
    %%CLOSE <streamid>

### Initiate stream

To initiate a logstream an `INITIATE STREAM` message is sent to the server,
which will be either accepted with a streaming ID or rejected.

Data required to initiate a stream:

* Local hostname
* File

Example for initiating a stream and receiving a stream ID:

    %%INIT;buildsrv1;/var/log/messages
    200 OK ID:231

In case of an error, no stream ID will be received.
A retry is allowed after 5s.

    %%INIT;buildsrv1;/var/log/messages
    500 ERR No space left

### Send stream

To send data on a stream each stream command will tell the 
server the size of data to expect.

    %%SEND <streamid>;<bytes>

The server will then gather this many bytes and then will start looking
for a command sequence again.

A bulk of log messages would be sent like this:

    %%INIT mymachine;/var/log/app.log
    !!OK ID:231
    %%SEND 231;183
    2018-02-12 17:33:11.231 [DEBUG] HTTP request received on eth0
    2018-02-12 17:33:11.237 [INFO] Processing GET /api/version
    2018-02-12 17:33:11.259 [INFO] Return 200 OK for /api/version
    !!OK
    %%SEND 231;129
    2018-02-12 17:33:12.992 [DEBUG] Schedule cleanup jobs
    2018-02-12 17:33:13.123 [INFO] Scheduled 5 jobs successfully
    !!OK

In the above example a stream is initialized and 2 send commands are executed
sending 5 lines of logs in total. Each send is acknowledged, so the client
knows that the lines have been received (and usually written) by the server.

### Close stream

A log stream can be closed using the CLOSE command. Further information on
the reason can be provided.

    %%CLOSE <streamid>;<code>;<reason>

For example:

    %CLOSE 231;200;client shutdown
    %CLOSE 231;400;file deleted

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
