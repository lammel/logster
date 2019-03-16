package logster

// ClientConfiguration is used to define the TOML config structure
type ClientConfiguration struct {
	Debug       bool
	Server      string
	FileStreams []stream `toml:"stream"`
}

// Stream holds the files to stream
type stream struct {
	Name string
	Path string
	Tag  string
}

type prometheusConfig struct {
	Listen  string
	Path    string
	Enabled bool
}

// ServerConfiguration is used to define the TOML config structure
type ServerConfiguration struct {
	Debug           bool
	Listen          string
	Prometheus      prometheusConfig
	OutputDirectory string                `toml:"directory"`
	OutputStreams   map[string]fileOutput `toml:"output"`
}

// FileOutput holds the files to stream
type fileOutput struct {
	Compress bool
	Path     string
}
