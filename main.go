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

	// Additional aircraft metrics - altitude
	metricAircraftAltGeom = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_alt_geom_feet",
		Help: "Aircraft geometric (GNSS/INS) altitude (feet)",
	}, []string{"hex", "flight", "category"})

	// Speed metrics
	metricAircraftIAS = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_ias_kts",
		Help: "Aircraft indicated air speed (knots)",
	}, []string{"hex", "flight", "category"})

	metricAircraftTAS = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_tas_kts",
		Help: "Aircraft true air speed (knots)",
	}, []string{"hex", "flight", "category"})

	metricAircraftMach = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_mach",
		Help: "Aircraft Mach number",
	}, []string{"hex", "flight", "category"})

	// Track and heading metrics
	metricAircraftTrack = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_track_deg",
		Help: "Aircraft true track over ground (degrees)",
	}, []string{"hex", "flight", "category"})

	metricAircraftTrackRate = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_track_rate_deg_per_sec",
		Help: "Aircraft rate of change of track (degrees/second)",
	}, []string{"hex", "flight", "category"})

	metricAircraftRoll = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_roll_deg",
		Help: "Aircraft roll angle (degrees, negative is left)",
	}, []string{"hex", "flight", "category"})

	metricAircraftMagHeading = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_mag_heading_deg",
		Help: "Aircraft magnetic heading (degrees)",
	}, []string{"hex", "flight", "category"})

	metricAircraftTrueHeading = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_true_heading_deg",
		Help: "Aircraft true heading (degrees)",
	}, []string{"hex", "flight", "category"})

	// Rate of climb/descent
	metricAircraftBaroRate = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_baro_rate_feet_per_min",
		Help: "Aircraft barometric altitude rate of change (feet/minute)",
	}, []string{"hex", "flight", "category"})

	metricAircraftGeomRate = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_geom_rate_feet_per_min",
		Help: "Aircraft geometric altitude rate of change (feet/minute)",
	}, []string{"hex", "flight", "category"})

	// Navigation metrics
	metricAircraftNavAltMCP = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_nav_altitude_mcp_feet",
		Help: "Aircraft selected altitude from MCP/FCU (feet)",
	}, []string{"hex", "flight", "category"})

	metricAircraftNavAltFMS = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_nav_altitude_fms_feet",
		Help: "Aircraft selected altitude from FMS (feet)",
	}, []string{"hex", "flight", "category"})

	metricAircraftNavModeActive = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_nav_mode_active",
		Help: "Aircraft navigation mode active (1=active, 0=inactive)",
	}, []string{"hex", "flight", "category", "mode"})

	// Quality and integrity metrics
	metricAircraftNIC = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_nic",
		Help: "Aircraft Navigation Integrity Category",
	}, []string{"hex", "flight", "category"})

	metricAircraftRC = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_rc_meters",
		Help: "Aircraft Radius of Containment (meters)",
	}, []string{"hex", "flight", "category"})

	metricAircraftNICBaro = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_nic_baro",
		Help: "Aircraft Navigation Integrity Category for Barometric Altitude",
	}, []string{"hex", "flight", "category"})

	metricAircraftNACP = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_nac_p",
		Help: "Aircraft Navigation Accuracy for Position",
	}, []string{"hex", "flight", "category"})

	metricAircraftNACV = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_nac_v",
		Help: "Aircraft Navigation Accuracy for Velocity",
	}, []string{"hex", "flight", "category"})

	metricAircraftSIL = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_sil",
		Help: "Aircraft Source Integrity Level",
	}, []string{"hex", "flight", "category"})

	metricAircraftGVA = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_gva",
		Help: "Aircraft Geometric Vertical Accuracy",
	}, []string{"hex", "flight", "category"})

	metricAircraftSDA = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_sda",
		Help: "Aircraft System Design Assurance",
	}, []string{"hex", "flight", "category"})

	metricAircraftVersion = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_version",
		Help: "Aircraft ADS-B Version Number",
	}, []string{"hex", "flight", "category"})

	// Timing metrics
	metricAircraftSeenPos = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_seen_pos_seconds",
		Help: "Seconds since last position update",
	}, []string{"hex", "flight", "category"})

	metricAircraftSeen = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_seen_seconds",
		Help: "Seconds since last message received",
	}, []string{"hex", "flight", "category"})

	metricAircraftMessages = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_messages_total",
		Help: "Total messages received from aircraft",
	}, []string{"hex", "flight", "category"})

	// Info metrics for string fields (as separate label-based metrics)
	metricAircraftSquawk = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_squawk_info",
		Help: "Aircraft squawk code (transponder code)",
	}, []string{"hex", "flight", "category", "squawk"})

	metricAircraftEmergency = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_emergency_info",
		Help: "Aircraft emergency status",
	}, []string{"hex", "flight", "category", "emergency"})

	metricAircraftSILTypeInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_aircraft_sil_type_info",
		Help: "Aircraft SIL type interpretation",
	}, []string{"hex", "flight", "category", "sil_type"})

	// Stats metrics - Local stats additional fields
	metricsLocalSamplesProcessed = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_local_samples_processed_total",
		Help: "Number of samples processed by local SDR",
	}, []string{"period"})

	metricsLocalSamplesDropped = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_local_samples_dropped_total",
		Help: "Number of samples dropped by local SDR",
	}, []string{"period"})

	metricsLocalModeAC = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_local_modeac_total",
		Help: "Number of Mode A/C messages decoded",
	}, []string{"period"})

	metricsLocalUnknownICAO = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_local_unknown_icao_total",
		Help: "Number of messages with unknown ICAO addresses",
	}, []string{"period"})

	metricsLocalAcceptedTotal = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_local_accepted_total",
		Help: "Total number of accepted messages",
	}, []string{"period"})

	metricsLocalAcceptedByErrors = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_local_accepted_by_errors",
		Help: "Number of accepted messages by error correction bits",
	}, []string{"period", "errors"})

	metricsLocalSignal = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_local_signal_dbfs",
		Help: "Mean signal power (dBFS)",
	}, []string{"period"})

	metricsLocalNoise = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_local_noise_dbfs",
		Help: "Mean noise power (dBFS)",
	}, []string{"period"})

	metricsLocalPeakSignal = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_local_peak_signal_dbfs",
		Help: "Peak signal power (dBFS)",
	}, []string{"period"})

	metricsLocalStrongSignals = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_local_strong_signals_total",
		Help: "Number of messages with strong signal (above -3dBFS)",
	}, []string{"period"})

	// Remote stats
	metricsRemoteModeAC = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_remote_modeac_total",
		Help: "Number of Mode A/C messages received remotely",
	}, []string{"period"})

	metricsRemoteModes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_remote_modes_total",
		Help: "Number of Mode S messages received remotely",
	}, []string{"period"})

	metricsRemoteBad = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_remote_bad_total",
		Help: "Number of bad messages received remotely",
	}, []string{"period"})

	metricsRemoteUnknownICAO = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_remote_unknown_icao_total",
		Help: "Number of remote messages with unknown ICAO",
	}, []string{"period"})

	metricsRemoteAcceptedTotal = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_remote_accepted_total",
		Help: "Total number of accepted remote messages",
	}, []string{"period"})

	metricsRemoteAcceptedByErrors = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_remote_accepted_by_errors",
		Help: "Number of accepted remote messages by error correction bits",
	}, []string{"period", "errors"})

	// CPR stats
	metricsCPRSurface = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_cpr_surface_total",
		Help: "Total surface CPR messages received",
	}, []string{"period"})

	metricsCPRAirborne = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_cpr_airborne_total",
		Help: "Total airborne CPR messages received",
	}, []string{"period"})

	metricsCPRGlobalOk = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_cpr_global_ok_total",
		Help: "Global positions successfully derived",
	}, []string{"period"})

	metricsCPRGlobalBad = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_cpr_global_bad_total",
		Help: "Global positions rejected (inconsistent)",
	}, []string{"period"})

	metricsCPRGlobalRange = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_cpr_global_range_total",
		Help: "Global positions rejected (exceeded max range)",
	}, []string{"period"})

	metricsCPRGlobalSpeed = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_cpr_global_speed_total",
		Help: "Global positions rejected (failed speed check)",
	}, []string{"period"})

	metricsCPRGlobalSkipped = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_cpr_global_skipped_total",
		Help: "Global position attempts skipped",
	}, []string{"period"})

	metricsCPRLocalOk = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_cpr_local_ok_total",
		Help: "Local positions successfully found",
	}, []string{"period"})

	metricsCPRLocalAircraftRelative = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_cpr_local_aircraft_relative_total",
		Help: "Local positions relative to previous aircraft position",
	}, []string{"period"})

	metricsCPRLocalReceiverRelative = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_cpr_local_receiver_relative_total",
		Help: "Local positions relative to receiver position",
	}, []string{"period"})

	metricsCPRLocalSkipped = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_cpr_local_skipped_total",
		Help: "Local position attempts skipped",
	}, []string{"period"})

	metricsCPRLocalRange = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_cpr_local_range_total",
		Help: "Local positions not used (exceeded range)",
	}, []string{"period"})

	metricsCPRLocalSpeed = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_cpr_local_speed_total",
		Help: "Local positions not used (failed speed check)",
	}, []string{"period"})

	metricsCPRFiltered = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_cpr_filtered_total",
		Help: "CPR messages filtered (faulty transponder)",
	}, []string{"period"})

	// Tracks stats
	metricsTracksAll = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_tracks_all_total",
		Help: "Total tracks created",
	}, []string{"period"})

	metricsTracksSingleMessage = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_tracks_single_message_total",
		Help: "Tracks with only single message",
	}, []string{"period"})

	metricsTracksUnreliable = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_tracks_unreliable_total",
		Help: "Tracks never marked as reliable",
	}, []string{"period"})

	// Altitude suppressed
	metricsAltitudeSuppressed = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "adsb_stats_altitude_suppressed_total",
		Help: "Number of altitude suppressed messages",
	}, []string{"period"})
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

	// register aircraft metrics
	prometheus.MustRegister(metricAircraftAltBaro)
	prometheus.MustRegister(metricAircraftAltGeom)
	prometheus.MustRegister(metricAircraftRssi)
	prometheus.MustRegister(metricAircraftGS)
	prometheus.MustRegister(metricAircraftIAS)
	prometheus.MustRegister(metricAircraftTAS)
	prometheus.MustRegister(metricAircraftMach)
	prometheus.MustRegister(metricAircraftTrack)
	prometheus.MustRegister(metricAircraftTrackRate)
	prometheus.MustRegister(metricAircraftRoll)
	prometheus.MustRegister(metricAircraftMagHeading)
	prometheus.MustRegister(metricAircraftTrueHeading)
	prometheus.MustRegister(metricAircraftBaroRate)
	prometheus.MustRegister(metricAircraftGeomRate)
	prometheus.MustRegister(metricAircraftLat)
	prometheus.MustRegister(metricAircraftLon)
	prometheus.MustRegister(metricAircraftNavQNH)
	prometheus.MustRegister(metricAircraftNavHeading)
	prometheus.MustRegister(metricAircraftNavAltMCP)
	prometheus.MustRegister(metricAircraftNavAltFMS)
	prometheus.MustRegister(metricAircraftNavModeActive)
	prometheus.MustRegister(metricAircraftNIC)
	prometheus.MustRegister(metricAircraftRC)
	prometheus.MustRegister(metricAircraftNICBaro)
	prometheus.MustRegister(metricAircraftNACP)
	prometheus.MustRegister(metricAircraftNACV)
	prometheus.MustRegister(metricAircraftSIL)
	prometheus.MustRegister(metricAircraftGVA)
	prometheus.MustRegister(metricAircraftSDA)
	prometheus.MustRegister(metricAircraftVersion)
	prometheus.MustRegister(metricAircraftSeenPos)
	prometheus.MustRegister(metricAircraftSeen)
	prometheus.MustRegister(metricAircraftMessages)
	prometheus.MustRegister(metricAircraftSquawk)
	prometheus.MustRegister(metricAircraftEmergency)
	prometheus.MustRegister(metricAircraftSILTypeInfo)

	// register additional local stats
	prometheus.MustRegister(metricsLocalSamplesProcessed)
	prometheus.MustRegister(metricsLocalSamplesDropped)
	prometheus.MustRegister(metricsLocalModeAC)
	prometheus.MustRegister(metricsLocalUnknownICAO)
	prometheus.MustRegister(metricsLocalAcceptedTotal)
	prometheus.MustRegister(metricsLocalAcceptedByErrors)
	prometheus.MustRegister(metricsLocalSignal)
	prometheus.MustRegister(metricsLocalNoise)
	prometheus.MustRegister(metricsLocalPeakSignal)
	prometheus.MustRegister(metricsLocalStrongSignals)

	// register remote stats
	prometheus.MustRegister(metricsRemoteModeAC)
	prometheus.MustRegister(metricsRemoteModes)
	prometheus.MustRegister(metricsRemoteBad)
	prometheus.MustRegister(metricsRemoteUnknownICAO)
	prometheus.MustRegister(metricsRemoteAcceptedTotal)
	prometheus.MustRegister(metricsRemoteAcceptedByErrors)

	// register CPR stats
	prometheus.MustRegister(metricsCPRSurface)
	prometheus.MustRegister(metricsCPRAirborne)
	prometheus.MustRegister(metricsCPRGlobalOk)
	prometheus.MustRegister(metricsCPRGlobalBad)
	prometheus.MustRegister(metricsCPRGlobalRange)
	prometheus.MustRegister(metricsCPRGlobalSpeed)
	prometheus.MustRegister(metricsCPRGlobalSkipped)
	prometheus.MustRegister(metricsCPRLocalOk)
	prometheus.MustRegister(metricsCPRLocalAircraftRelative)
	prometheus.MustRegister(metricsCPRLocalReceiverRelative)
	prometheus.MustRegister(metricsCPRLocalSkipped)
	prometheus.MustRegister(metricsCPRLocalRange)
	prometheus.MustRegister(metricsCPRLocalSpeed)
	prometheus.MustRegister(metricsCPRFiltered)

	// register tracks stats
	prometheus.MustRegister(metricsTracksAll)
	prometheus.MustRegister(metricsTracksSingleMessage)
	prometheus.MustRegister(metricsTracksUnreliable)

	// register altitude suppressed
	prometheus.MustRegister(metricsAltitudeSuppressed)
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

	// Local stats
	if p.Local != nil {
		metricsLocalModes.WithLabelValues(name).Set(float64(p.Local.Modes))
		metricsLocalBad.WithLabelValues(name).Set(float64(p.Local.Bad))
		metricsLocalSamplesProcessed.WithLabelValues(name).Set(float64(p.Local.SamplesProcessed))
		metricsLocalSamplesDropped.WithLabelValues(name).Set(float64(p.Local.SamplesDropped))
		metricsLocalModeAC.WithLabelValues(name).Set(float64(p.Local.ModeAC))
		metricsLocalUnknownICAO.WithLabelValues(name).Set(float64(p.Local.UnknownICAO))

		// Accepted messages - total and by error correction bits
		if len(p.Local.Accepted) > 0 {
			var total int64
			for i, count := range p.Local.Accepted {
				total += count
				metricsLocalAcceptedByErrors.WithLabelValues(name, strconv.Itoa(i)).Set(float64(count))
			}
			metricsLocalAcceptedTotal.WithLabelValues(name).Set(float64(total))
		}

		if p.Local.Signal != nil {
			metricsLocalSignal.WithLabelValues(name).Set(*p.Local.Signal)
		}
		if p.Local.Noise != nil {
			metricsLocalNoise.WithLabelValues(name).Set(*p.Local.Noise)
		}
		if p.Local.PeakSignal != nil {
			metricsLocalPeakSignal.WithLabelValues(name).Set(*p.Local.PeakSignal)
		}
		metricsLocalStrongSignals.WithLabelValues(name).Set(float64(p.Local.StrongSignals))

		if p.Local.GainDB != nil {
			metricsLocalGainDB.WithLabelValues(name).Set(*p.Local.GainDB)
		}
	}

	// Remote stats
	if p.Remote != nil {
		metricsRemoteModeAC.WithLabelValues(name).Set(float64(p.Remote.ModeAC))
		metricsRemoteModes.WithLabelValues(name).Set(float64(p.Remote.Modes))
		metricsRemoteBad.WithLabelValues(name).Set(float64(p.Remote.Bad))
		metricsRemoteUnknownICAO.WithLabelValues(name).Set(float64(p.Remote.UnknownICAO))

		if len(p.Remote.Accepted) > 0 {
			var total int64
			for i, count := range p.Remote.Accepted {
				total += count
				metricsRemoteAcceptedByErrors.WithLabelValues(name, strconv.Itoa(i)).Set(float64(count))
			}
			metricsRemoteAcceptedTotal.WithLabelValues(name).Set(float64(total))
		}
	}

	// CPU metrics
	if p.CPU != nil {
		metricsCPUDemod.WithLabelValues(name).Set(float64(p.CPU.Demod))
		metricsCPUReader.WithLabelValues(name).Set(float64(p.CPU.Reader))
		metricsCPUBackground.WithLabelValues(name).Set(float64(p.CPU.Background))
	}

	// CPR stats
	if p.CPR != nil {
		metricsCPRSurface.WithLabelValues(name).Set(float64(p.CPR.Surface))
		metricsCPRAirborne.WithLabelValues(name).Set(float64(p.CPR.Airborne))
		metricsCPRGlobalOk.WithLabelValues(name).Set(float64(p.CPR.GlobalOk))
		metricsCPRGlobalBad.WithLabelValues(name).Set(float64(p.CPR.GlobalBad))
		metricsCPRGlobalRange.WithLabelValues(name).Set(float64(p.CPR.GlobalRange))
		metricsCPRGlobalSpeed.WithLabelValues(name).Set(float64(p.CPR.GlobalSpeed))
		metricsCPRGlobalSkipped.WithLabelValues(name).Set(float64(p.CPR.GlobalSkipped))
		metricsCPRLocalOk.WithLabelValues(name).Set(float64(p.CPR.LocalOk))
		metricsCPRLocalAircraftRelative.WithLabelValues(name).Set(float64(p.CPR.LocalAircraftRel))
		metricsCPRLocalReceiverRelative.WithLabelValues(name).Set(float64(p.CPR.LocalReceiverRel))
		metricsCPRLocalSkipped.WithLabelValues(name).Set(float64(p.CPR.LocalSkipped))
		metricsCPRLocalRange.WithLabelValues(name).Set(float64(p.CPR.LocalRange))
		metricsCPRLocalSpeed.WithLabelValues(name).Set(float64(p.CPR.LocalSpeed))
		metricsCPRFiltered.WithLabelValues(name).Set(float64(p.CPR.Filtered))
	}

	// Tracks stats
	if p.Tracks != nil {
		if all, ok := p.Tracks["all"]; ok {
			metricsTracksAll.WithLabelValues(name).Set(float64(all))
		}
		if single, ok := p.Tracks["single_message"]; ok {
			metricsTracksSingleMessage.WithLabelValues(name).Set(float64(single))
		}
		if unreliable, ok := p.Tracks["unreliable"]; ok {
			metricsTracksUnreliable.WithLabelValues(name).Set(float64(unreliable))
		}
	}

	// Adaptive metrics
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

	// Messages by DF
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

		// Altitude metrics
		if n, ok := numericFromInterface(ac.AltBaro); ok {
			metricAircraftAltBaro.With(labels).Set(n)
		}
		if n, ok := numericFromInterface(ac.AltGeom); ok {
			metricAircraftAltGeom.With(labels).Set(n)
		}

		// Speed metrics
		if ac.GS != nil {
			metricAircraftGS.With(labels).Set(*ac.GS)
		}
		if ac.IAS != nil {
			metricAircraftIAS.With(labels).Set(*ac.IAS)
		}
		if ac.TAS != nil {
			metricAircraftTAS.With(labels).Set(*ac.TAS)
		}
		if ac.Mach != nil {
			metricAircraftMach.With(labels).Set(*ac.Mach)
		}

		// Track and heading metrics
		if ac.Track != nil {
			metricAircraftTrack.With(labels).Set(*ac.Track)
		}
		if ac.TrackRate != nil {
			metricAircraftTrackRate.With(labels).Set(*ac.TrackRate)
		}
		if ac.Roll != nil {
			metricAircraftRoll.With(labels).Set(*ac.Roll)
		}
		if ac.MagHeading != nil {
			metricAircraftMagHeading.With(labels).Set(*ac.MagHeading)
		}
		if ac.TrueHeading != nil {
			metricAircraftTrueHeading.With(labels).Set(*ac.TrueHeading)
		}

		// Rate of climb/descent
		if ac.BaroRate != nil {
			metricAircraftBaroRate.With(labels).Set(*ac.BaroRate)
		}
		if ac.GeomRate != nil {
			metricAircraftGeomRate.With(labels).Set(*ac.GeomRate)
		}

		// Position
		if ac.Lat != nil {
			metricAircraftLat.With(labels).Set(*ac.Lat)
		}
		if ac.Lon != nil {
			metricAircraftLon.With(labels).Set(*ac.Lon)
		}

		// Navigation metrics
		if ac.NavQNH != nil {
			metricAircraftNavQNH.With(labels).Set(*ac.NavQNH)
		}
		if ac.NavHeading != nil {
			metricAircraftNavHeading.With(labels).Set(*ac.NavHeading)
		}
		if ac.NavAltMCP != nil {
			metricAircraftNavAltMCP.With(labels).Set(*ac.NavAltMCP)
		}
		if ac.NavAltFMS != nil {
			metricAircraftNavAltFMS.With(labels).Set(*ac.NavAltFMS)
		}

		// Nav modes - convert array to individual boolean metrics
		if ac.NavModes != nil {
			modes := make(map[string]bool)
			// Parse nav_modes which can be array of strings or empty array
			if modeArray, ok := ac.NavModes.([]interface{}); ok {
				for _, m := range modeArray {
					if modeStr, ok := m.(string); ok {
						modes[modeStr] = true
					}
				}
			}
			// Set all possible modes
			possibleModes := []string{"autopilot", "vnav", "althold", "approach", "lnav", "tcas"}
			for _, mode := range possibleModes {
				modeLabels := prometheus.Labels{
					"hex":      hex,
					"flight":   flight,
					"category": category,
					"mode":     mode,
				}
				if modes[mode] {
					metricAircraftNavModeActive.With(modeLabels).Set(1)
				} else {
					metricAircraftNavModeActive.With(modeLabels).Set(0)
				}
			}
		}

		// Quality and integrity metrics
		if ac.NIC != nil {
			metricAircraftNIC.With(labels).Set(float64(*ac.NIC))
		}
		if ac.RC != nil {
			metricAircraftRC.With(labels).Set(float64(*ac.RC))
		}
		if ac.NICBaro != nil {
			metricAircraftNICBaro.With(labels).Set(float64(*ac.NICBaro))
		}
		if ac.NACP != nil {
			metricAircraftNACP.With(labels).Set(float64(*ac.NACP))
		}
		if ac.NACV != nil {
			metricAircraftNACV.With(labels).Set(float64(*ac.NACV))
		}
		if ac.SIL != nil {
			metricAircraftSIL.With(labels).Set(float64(*ac.SIL))
		}
		if ac.GVA != nil {
			metricAircraftGVA.With(labels).Set(float64(*ac.GVA))
		}
		if ac.SDA != nil {
			metricAircraftSDA.With(labels).Set(float64(*ac.SDA))
		}
		if ac.Version != nil {
			metricAircraftVersion.With(labels).Set(float64(*ac.Version))
		}

		// Timing metrics
		if ac.SeenPos != nil {
			metricAircraftSeenPos.With(labels).Set(*ac.SeenPos)
		}
		if ac.Seen != nil {
			metricAircraftSeen.With(labels).Set(*ac.Seen)
		}
		metricAircraftMessages.With(labels).Set(float64(ac.Messages))

		// RSSI
		if ac.RSSI != nil {
			metricAircraftRssi.With(labels).Set(*ac.RSSI)
		}

		// Info metrics for string fields (as separate metrics)
		if ac.Squawk != "" {
			squawkLabels := prometheus.Labels{
				"hex":      hex,
				"flight":   flight,
				"category": category,
				"squawk":   ac.Squawk,
			}
			metricAircraftSquawk.With(squawkLabels).Set(1)
		}

		if ac.Emergency != "" {
			emergencyLabels := prometheus.Labels{
				"hex":       hex,
				"flight":    flight,
				"category":  category,
				"emergency": ac.Emergency,
			}
			metricAircraftEmergency.With(emergencyLabels).Set(1)
		}

		if ac.SILType != "" {
			silTypeLabels := prometheus.Labels{
				"hex":      hex,
				"flight":   flight,
				"category": category,
				"sil_type": ac.SILType,
			}
			metricAircraftSILTypeInfo.With(silTypeLabels).Set(1)
		}
	}

	// delete stale labels that were present previously but not in current set
	prevAircraftLabelsMu.Lock()
	defer prevAircraftLabelsMu.Unlock()

	for k, labels := range prevAircraftLabels {
		if _, ok := cur[k]; !ok {
			// Delete all metrics for this aircraft
			metricAircraftAltBaro.Delete(labels)
			metricAircraftAltGeom.Delete(labels)
			metricAircraftRssi.Delete(labels)
			metricAircraftGS.Delete(labels)
			metricAircraftIAS.Delete(labels)
			metricAircraftTAS.Delete(labels)
			metricAircraftMach.Delete(labels)
			metricAircraftTrack.Delete(labels)
			metricAircraftTrackRate.Delete(labels)
			metricAircraftRoll.Delete(labels)
			metricAircraftMagHeading.Delete(labels)
			metricAircraftTrueHeading.Delete(labels)
			metricAircraftBaroRate.Delete(labels)
			metricAircraftGeomRate.Delete(labels)
			metricAircraftLat.Delete(labels)
			metricAircraftLon.Delete(labels)
			metricAircraftNavQNH.Delete(labels)
			metricAircraftNavHeading.Delete(labels)
			metricAircraftNavAltMCP.Delete(labels)
			metricAircraftNavAltFMS.Delete(labels)
			metricAircraftNIC.Delete(labels)
			metricAircraftRC.Delete(labels)
			metricAircraftNICBaro.Delete(labels)
			metricAircraftNACP.Delete(labels)
			metricAircraftNACV.Delete(labels)
			metricAircraftSIL.Delete(labels)
			metricAircraftGVA.Delete(labels)
			metricAircraftSDA.Delete(labels)
			metricAircraftVersion.Delete(labels)
			metricAircraftSeenPos.Delete(labels)
			metricAircraftSeen.Delete(labels)
			metricAircraftMessages.Delete(labels)

			// Delete nav mode metrics
			for _, mode := range []string{"autopilot", "vnav", "althold", "approach", "lnav", "tcas"} {
				modeLabels := prometheus.Labels{
					"hex":      labels["hex"],
					"flight":   labels["flight"],
					"category": labels["category"],
					"mode":     mode,
				}
				metricAircraftNavModeActive.Delete(modeLabels)
			}

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
