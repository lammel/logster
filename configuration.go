package loghamster

// Configuration is used to define the TOML config structure
type Configuration struct {
	Debug      bool
	Mode       string
	Server     ServerConfig
	Target     TargetConfig
	Input      []fileInput
	Output     []fileOutput
	Prometheus PrometheusConfig
	Syslog     SyslogConfig
	Profile    ProfileConfig
}

// ServerConfig for settings of a loghamster in server/receiver mode
type ServerConfig struct {
	ListenAddress string `default:":7007"`
	BaseDirectory string `default:"/var/log/loghamster"`
	PathTemplate  string `default:"$HOST/$FILE"`
}

// TargetConfig for settings of a loghamster in client/sender mode
type TargetConfig struct {
	Hostname string
	Port     int  `default:":7007"`
	Compress bool `default:"true"`
}

// Stream holds the files to stream
type fileInput struct {
	Name  string
	Path  string
	Watch bool
}

type fileOutput struct {
	Name           string
	Path           string
	Compress       bool
	CompressMethod string
	Rotate         int
}

// PrometheusConfig holds configuration for a Prometheus /metrics endpoint
type PrometheusConfig struct {
	Listen  string `default:":9081"`
	Path    string `default:"/metrics"`
	Enabled bool   `default:"false"`
}

// SyslogConfig holds the logging configuration
type SyslogConfig struct {
	Enabled         bool   `default:"false"`
	ApplicationName string `default:"loghamster"`
	Hostname        string // "hostname:port" of syslog server, if empty or not provided unix syslog will used
}

// ProfileConfig allows enabling of the internal CPU/Memory profiler
type ProfileConfig struct {
	Enabled bool
	Port    string `default:":9293"`
}
