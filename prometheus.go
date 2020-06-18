package loghamster

import (
	"net/http"

	"github.com/VictoriaMetrics/metrics"
	"github.com/rs/zerolog/log"
)

// Metric counters
var (
	// Number of active clients sending data
	metricClientsActive = metrics.NewCounter("loghamster_clients_active")
	// Number of connected (active and idle) clients
	metricClientsConnected = metrics.NewCounter("loghamster_clients_connected")
	// Total number of connections since start
	metricClientConnectsTotal = metrics.NewCounter("loghamster_connections_total")
	// Total number of bytes received since start
	metricBytesRecvTotal = metrics.NewCounter("loghamster_bytes_received_total")
)

// ListenPrometheus will provide application metrics via HTTP under e/metrics
func ListenPrometheus(listen string) {
	// Register the summary and the histogram with Prometheus's default registry.

	log.Debug().Str("listen", listen).Msg("Starting prometheus metrics provider")
	http.HandleFunc("/metrics", func(w http.ResponseWriter, req *http.Request) {
		metrics.WritePrometheus(w, true)
	})
	err := http.ListenAndServe(listen, nil)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to listen for prometheus HTTP")
	} else {
		log.Info().Str("listen", listen).Msg("Listen for prometheus metrics on ")
	}
}
