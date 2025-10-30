package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Simplified structures for stats.json
type StatsPeriod struct {
	Start        float64        `json:"start"`
	End          float64        `json:"end"`
	Local        *LocalStats    `json:"local,omitempty"`
	Remote       *RemoteStats   `json:"remote,omitempty"`
	CPU          *CPUStats      `json:"cpu,omitempty"`
	CPR          *CPRStats      `json:"cpr,omitempty"`
	Tracks       map[string]int `json:"tracks,omitempty"`
	Messages     int            `json:"messages,omitempty"`
	MessagesByDF []int          `json:"messages_by_df,omitempty"`
	Adaptive     *AdaptiveStats `json:"adaptive,omitempty"`
}

type LocalStats struct {
	SamplesProcessed int64    `json:"samples_processed,omitempty"`
	SamplesDropped   int64    `json:"samples_dropped,omitempty"`
	ModeAC           int64    `json:"modeac,omitempty"`
	Modes            int64    `json:"modes,omitempty"`
	Bad              int64    `json:"bad,omitempty"`
	UnknownICAO      int64    `json:"unknown_icao,omitempty"`
	Accepted         []int64  `json:"accepted,omitempty"`
	Signal           *float64 `json:"signal,omitempty"`
	Noise            *float64 `json:"noise,omitempty"`
	PeakSignal       *float64 `json:"peak_signal,omitempty"`
	StrongSignals    int64    `json:"strong_signals,omitempty"`
	GainDB           *float64 `json:"gain_db,omitempty"`
}

type RemoteStats = LocalStats

type CPUStats struct {
	Demod      int64 `json:"demod,omitempty"`
	Reader     int64 `json:"reader,omitempty"`
	Background int64 `json:"background,omitempty"`
}

type CPRStats struct {
	Surface          int64 `json:"surface,omitempty"`
	Airborne         int64 `json:"airborne,omitempty"`
	GlobalOk         int64 `json:"global_ok,omitempty"`
	GlobalBad        int64 `json:"global_bad,omitempty"`
	GlobalRange      int64 `json:"global_range,omitempty"`
	GlobalSpeed      int64 `json:"global_speed,omitempty"`
	GlobalSkipped    int64 `json:"global_skipped,omitempty"`
	LocalOk          int64 `json:"local_ok,omitempty"`
	LocalAircraftRel int64 `json:"local_aircraft_relative,omitempty"`
	LocalReceiverRel int64 `json:"local_receiver_relative,omitempty"`
	LocalSkipped     int64 `json:"local_skipped,omitempty"`
	LocalRange       int64 `json:"local_range,omitempty"`
	LocalSpeed       int64 `json:"local_speed,omitempty"`
	Filtered         int64 `json:"filtered,omitempty"`
}

// Adaptive gain stats (see README-json.md)
type AdaptiveStats struct {
	GainDB              *float64 `json:"gain_db,omitempty"`
	DynamicRangeLimitDB *float64 `json:"dynamic_range_limit_db,omitempty"`
	GainChanges         *int64   `json:"gain_changes,omitempty"`
	LoudUndecoded       *int64   `json:"loud_undecoded,omitempty"`
	LoudDecoded         *int64   `json:"loud_decoded,omitempty"`
	NoiseDBFS           *float64 `json:"noise_dbfs,omitempty"`
	// gain_seconds keyed by integer gain step; value is [gain_db (float), seconds (number)]
	GainSeconds map[string][]interface{} `json:"gain_seconds,omitempty"`
}

type Stats struct {
	Latest    StatsPeriod `json:"latest"`
	Last1Min  StatsPeriod `json:"last1min"`
	Last5Min  StatsPeriod `json:"last5min"`
	Last15Min StatsPeriod `json:"last15min"`
	Total     StatsPeriod `json:"total"`
}

// aircrafts.json structures
type Aircraft struct {
	Hex         string      `json:"hex"`
	Flight      string      `json:"flight,omitempty"`
	AltBaro     interface{} `json:"alt_baro,omitempty"`
	AltGeom     interface{} `json:"alt_geom,omitempty"`
	GS          *float64    `json:"gs,omitempty"`
	IAS         *float64    `json:"ias,omitempty"`
	TAS         *float64    `json:"tas,omitempty"`
	Mach        *float64    `json:"mach,omitempty"`
	Track       *float64    `json:"track,omitempty"`
	TrackRate   *float64    `json:"track_rate,omitempty"`
	Roll        *float64    `json:"roll,omitempty"`
	MagHeading  *float64    `json:"mag_heading,omitempty"`
	TrueHeading *float64    `json:"true_heading,omitempty"`
	BaroRate    *float64    `json:"baro_rate,omitempty"`
	GeomRate    *float64    `json:"geom_rate,omitempty"`
	Squawk      string      `json:"squawk,omitempty"`
	Emergency   string      `json:"emergency,omitempty"`
	Category    string      `json:"category,omitempty"`
	NavQNH      *float64    `json:"nav_qnh,omitempty"`
	NavAltMCP   *float64    `json:"nav_altitude_mcp,omitempty"`
	NavAltFMS   *float64    `json:"nav_altitude_fms,omitempty"`
	NavHeading  *float64    `json:"nav_heading,omitempty"`
	NavModes    interface{} `json:"nav_modes,omitempty"`
	Lat         *float64    `json:"lat,omitempty"`
	Lon         *float64    `json:"lon,omitempty"`
	NIC         *int        `json:"nic,omitempty"`
	RC          *int        `json:"rc,omitempty"`
	SeenPos     *float64    `json:"seen_pos,omitempty"`
	Version     *int        `json:"version,omitempty"`
	NICBaro     *int        `json:"nic_baro,omitempty"`
	NACP        *int        `json:"nac_p,omitempty"`
	NACV        *int        `json:"nac_v,omitempty"`
	SIL         *int        `json:"sil,omitempty"`
	SILType     string      `json:"sil_type,omitempty"`
	GVA         *int        `json:"gva,omitempty"`
	SDA         *int        `json:"sda,omitempty"`
	Messages    int         `json:"messages,omitempty"`
	Seen        *float64    `json:"seen,omitempty"`
	RSSI        *float64    `json:"rssi,omitempty"`
	MLAT        interface{} `json:"mlat,omitempty"`
	TISB        interface{} `json:"tisb,omitempty"`
}

type AircraftsFile struct {
	Now      float64    `json:"now"`
	Messages int        `json:"messages"`
	Aircraft []Aircraft `json:"aircraft"`
}

// Prometheus metrics
var (
	metricsMessages = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_messages_total",
		Help: "Number of messages for given stats period",
	}, []string{"period"})

	metricsLocalModes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_local_modes_total",
		Help: "Local modes (modes) count by period",
	}, []string{"period"})

	metricsLocalBad = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_local_bad_total",
		Help: "Local bad messages count by period",
	}, []string{"period"})

	metricsMessagesByDF = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_messages_by_df",
		Help: "Messages per DF for a given period",
	}, []string{"period", "df"})

	// CPU metrics (milliseconds)
	metricsCPUDemod = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_cpu_demod_ms",
		Help: "Milliseconds spent doing demodulation (per period)",
	}, []string{"period"})
	metricsCPUReader = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_cpu_reader_ms",
		Help: "Milliseconds spent reading samples from SDR (per period)",
	}, []string{"period"})
	metricsCPUBackground = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_cpu_background_ms",
		Help: "Milliseconds spent in background processing (per period)",
	}, []string{"period"})

	// local gain
	metricsLocalGainDB = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_local_gain_db",
		Help: "SDR gain reported under stats.local.gain_db (dB)",
	}, []string{"period"})

	// adaptive metrics
	metricsAdaptiveGainDB = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_adaptive_gain_db",
		Help: "Adaptive latest SDR gain (legacy; prefer local.gain_db) (dB)",
	}, []string{"period"})
	metricsAdaptiveDynamicRangeLimitDB = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_adaptive_dynamic_range_limit_db",
		Help: "Adaptive dynamic range limit (dB)",
	}, []string{"period"})
	metricsAdaptiveGainChanges = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_adaptive_gain_changes_total",
		Help: "Number of adaptive gain changes in this period",
	}, []string{"period"})
	metricsAdaptiveLoudUndecoded = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_adaptive_loud_undecoded_total",
		Help: "Number of loud undecoded bursts seen",
	}, []string{"period"})
	metricsAdaptiveLoudDecoded = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_adaptive_loud_decoded_total",
		Help: "Number of loud decoded messages seen",
	}, []string{"period"})
	metricsAdaptiveNoiseDBFS = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_adaptive_noise_dbfs",
		Help: "Adaptive noise floor estimate (dBFS)",
	}, []string{"period"})
	// gain_seconds: period, gain_step, gain_db -> seconds
	metricsAdaptiveGainSeconds = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_adaptive_gain_seconds",
		Help: "Number of seconds spent at a given adaptive gain step",
	}, []string{"period", "gain_step", "gain_db"})

	metricAircraftAltBaro = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_alt_baro_feet",
		Help: "Aircraft barometric altitude (feet)",
	}, []string{"hex", "flight", "category"})

	metricAircraftRssi = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_rssi_dbfs",
		Help: "Recent average RSSI (dBFS)",
	}, []string{"hex", "flight", "category"})

	metricAircraftGS = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_ground_speed_kts",
		Help: "Aircraft ground speed (knots)",
	}, []string{"hex", "flight", "category"})

	metricAircraftLat = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_lat",
		Help: "Aircraft latitude",
	}, []string{"hex", "flight", "category"})

	metricAircraftLon = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_lon",
		Help: "Aircraft longitude",
	}, []string{"hex", "flight", "category"})

	metricAircraftNavQNH = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_nav_qnh_hpa",
		Help: "Aircraft nav QNH (hPa)",
	}, []string{"hex", "flight", "category"})
	metricAircraftNavHeading = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_nav_heading_deg",
		Help: "Aircraft selected nav heading (degrees)",
	}, []string{"hex", "flight", "category"})
)

// previous aircraft labels tracking for deletion of stale metrics
var (
	prevAircraftLabelsMu sync.Mutex
	prevAircraftLabels   = map[string]prometheus.Labels{}
)

func init() {
	prometheus.MustRegister(metricsMessages)
	prometheus.MustRegister(metricsLocalModes)
	prometheus.MustRegister(metricsLocalBad)
	prometheus.MustRegister(metricsMessagesByDF)

	// register CPU metrics
	prometheus.MustRegister(metricsCPUDemod)
	prometheus.MustRegister(metricsCPUReader)
	prometheus.MustRegister(metricsCPUBackground)

	// register local/adaptive metrics
	prometheus.MustRegister(metricsLocalGainDB)
	prometheus.MustRegister(metricsAdaptiveGainDB)
	prometheus.MustRegister(metricsAdaptiveDynamicRangeLimitDB)
	prometheus.MustRegister(metricsAdaptiveGainChanges)
	prometheus.MustRegister(metricsAdaptiveLoudUndecoded)
	prometheus.MustRegister(metricsAdaptiveLoudDecoded)
	prometheus.MustRegister(metricsAdaptiveNoiseDBFS)
	prometheus.MustRegister(metricsAdaptiveGainSeconds)

	prometheus.MustRegister(metricAircraftAltBaro)
	prometheus.MustRegister(metricAircraftRssi)
	prometheus.MustRegister(metricAircraftGS)
	prometheus.MustRegister(metricAircraftLat)
	prometheus.MustRegister(metricAircraftLon)
	prometheus.MustRegister(metricAircraftNavQNH)
	prometheus.MustRegister(metricAircraftNavHeading)
}

func safeReadFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func updateStatsFromFile(path string) error {
	b, err := safeReadFile(path)
	if err != nil {
		return err
	}
	var s Stats
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("unmarshal stats: %w", err)
	}

	applyStatsPeriod("latest", &s.Latest)
	applyStatsPeriod("last1min", &s.Last1Min)
	applyStatsPeriod("last5min", &s.Last5Min)
	applyStatsPeriod("last15min", &s.Last15Min)
	applyStatsPeriod("total", &s.Total)
	return nil
}

func applyStatsPeriod(name string, p *StatsPeriod) {
	if p == nil {
		return
	}
	metricsMessages.WithLabelValues(name).Set(float64(p.Messages))
	if p.Local != nil {
		metricsLocalModes.WithLabelValues(name).Set(float64(p.Local.Modes))
		metricsLocalBad.WithLabelValues(name).Set(float64(p.Local.Bad))
		if p.Local.GainDB != nil {
			metricsLocalGainDB.WithLabelValues(name).Set(*p.Local.GainDB)
		}
	}
	// CPU metrics (if present)
	if p.CPU != nil {
		metricsCPUDemod.WithLabelValues(name).Set(float64(p.CPU.Demod))
		metricsCPUReader.WithLabelValues(name).Set(float64(p.CPU.Reader))
		metricsCPUBackground.WithLabelValues(name).Set(float64(p.CPU.Background))
	}
	// adaptive metrics
	if p.Adaptive != nil {
		if p.Adaptive.GainDB != nil {
			metricsAdaptiveGainDB.WithLabelValues(name).Set(*p.Adaptive.GainDB)
		}
		if p.Adaptive.DynamicRangeLimitDB != nil {
			metricsAdaptiveDynamicRangeLimitDB.WithLabelValues(name).Set(*p.Adaptive.DynamicRangeLimitDB)
		}
		if p.Adaptive.GainChanges != nil {
			metricsAdaptiveGainChanges.WithLabelValues(name).Set(float64(*p.Adaptive.GainChanges))
		}
		if p.Adaptive.LoudUndecoded != nil {
			metricsAdaptiveLoudUndecoded.WithLabelValues(name).Set(float64(*p.Adaptive.LoudUndecoded))
		}
		if p.Adaptive.LoudDecoded != nil {
			metricsAdaptiveLoudDecoded.WithLabelValues(name).Set(float64(*p.Adaptive.LoudDecoded))
		}
		if p.Adaptive.NoiseDBFS != nil {
			metricsAdaptiveNoiseDBFS.WithLabelValues(name).Set(*p.Adaptive.NoiseDBFS)
		}
		// gain_seconds: map[string][]interface{} -> [gain_db, seconds]
		for step, arr := range p.Adaptive.GainSeconds {
			if len(arr) >= 2 {
				if g, ok := numericFromInterface(arr[0]); ok {
					if secs, ok2 := numericFromInterface(arr[1]); ok2 {
						metricsAdaptiveGainSeconds.WithLabelValues(name, step, fmt.Sprintf("%v", g)).Set(secs)
					}
				}
			}
		}
	}
	if p.MessagesByDF != nil {
		for i, v := range p.MessagesByDF {
			metricsMessagesByDF.WithLabelValues(name, strconv.Itoa(i)).Set(float64(v))
		}
	}
}

func updateAircraftsFromFile(path string) error {
	b, err := safeReadFile(path)
	if err != nil {
		return err
	}
	var a AircraftsFile
	if err := json.Unmarshal(b, &a); err != nil {
		return fmt.Errorf("unmarshal aircrafts: %w", err)
	}

	// build current label set
	cur := map[string]prometheus.Labels{}

	for _, ac := range a.Aircraft {
		hex := ac.Hex
		flight := ac.Flight
		category := ac.Category

		labels := prometheus.Labels{"hex": hex, "flight": flight, "category": category}
		key := hex + "|" + flight + "|" + category
		cur[key] = labels

		if n, ok := numericFromInterface(ac.AltBaro); ok {
			metricAircraftAltBaro.With(labels).Set(n)
		} else {
			// If no alt_baro, try to reset to 0 to avoid stale values (optional)
			// metricAircraftAltBaro.With(labels).Set(0)
		}
		if ac.RSSI != nil {
			metricAircraftRssi.With(labels).Set(*ac.RSSI)
		}
		if ac.GS != nil {
			metricAircraftGS.With(labels).Set(*ac.GS)
		}
		if ac.Lat != nil {
			metricAircraftLat.With(labels).Set(*ac.Lat)
		}
		if ac.Lon != nil {
			metricAircraftLon.With(labels).Set(*ac.Lon)
		}
		// nav fields
		if ac.NavQNH != nil {
			metricAircraftNavQNH.With(labels).Set(*ac.NavQNH)
		}
		if ac.NavHeading != nil {
			metricAircraftNavHeading.With(labels).Set(*ac.NavHeading)
		}
	}

	// delete stale labels that were present previously but not in current set
	prevAircraftLabelsMu.Lock()
	defer prevAircraftLabelsMu.Unlock()

	for k, labels := range prevAircraftLabels {
		if _, ok := cur[k]; !ok {
			metricAircraftAltBaro.Delete(labels)
			metricAircraftRssi.Delete(labels)
			metricAircraftGS.Delete(labels)
			metricAircraftLat.Delete(labels)
			metricAircraftLon.Delete(labels)
			delete(prevAircraftLabels, k)
		}
	}

	// replace previous set with current
	for k, v := range cur {
		prevAircraftLabels[k] = v
	}

	return nil
}

func numericFromInterface(v interface{}) (float64, bool) {
	if v == nil {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return f, true
		}
	case string:
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	statsPath := getenv("STATS_PATH", "stats.json")
	aircraftsPath := getenv("AIRCRAFTS_PATH", "aircrafts.json")
	listenAddr := getenv("LISTEN_ADDR", ":9187")
	intervalSecStr := getenv("INTERVAL_SECONDS", "5")
	intervalSec, err := strconv.Atoi(intervalSecStr)
	if err != nil || intervalSec <= 0 {
		log.Printf("invalid INTERVAL_SECONDS=%q, using 5", intervalSecStr)
		intervalSec = 5
	}
	interval := time.Duration(intervalSec) * time.Second

	// initial load
	if err := updateStatsFromFile(statsPath); err != nil {
		log.Printf("initial stats load failed: %v", err)
	}
	if err := updateAircraftsFromFile(aircraftsPath); err != nil {
		log.Printf("initial aircrafts load failed: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := updateStatsFromFile(statsPath); err != nil {
					log.Printf("reload stats failed: %v", err)
				}
				if err := updateAircraftsFromFile(aircraftsPath); err != nil {
					log.Printf("reload aircrafts failed: %v", err)
				}
			}
		}
	}()

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	server := &http.Server{
		Addr:    listenAddr,
		Handler: mux,
	}

	go func() {
		log.Printf("starting metrics server on %s", listenAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("metrics server failed: %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("shutdown signal received, shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
	log.Printf("exited")
}
