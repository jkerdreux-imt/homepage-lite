package main

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v2"

	"github.com/fsnotify/fsnotify"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
)

// Build information (set via ldflags)
var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
	GoVersion = "unknown"
)

// Config file path (set via flag)
var configPath string

// Templates and Config variables
var (
	templates    *template.Template
	config       Config
	configMutex  sync.RWMutex
	sseClients   map[chan string]bool
	sseClientsMu sync.Mutex
)

// Service status store (updated by background goroutine)
var (
	serviceStatus    = make(map[string]string)
	serviceStatusMux sync.RWMutex
)

// Global HTTP client for service status checks
var httpClient = &http.Client{
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	},
	Timeout: 5 * time.Second,
}

// getServiceID generates a short hash ID from a service URL
func getServiceID(url string) string {
	hash := sha256.Sum256([]byte(url))
	return hex.EncodeToString(hash[:])[:8]
}

func init() {
	sseClients = make(map[chan string]bool)
}

type Config struct {
	Services  []ServiceGroup  `yaml:"services"`
	Bookmarks []BookmarkGroup `yaml:"bookmarks"`
	Settings  Settings        `yaml:"settings"`
}

type ServiceGroup struct {
	Group string    `yaml:"group"`
	Items []Service `yaml:"items"`
}

type Service struct {
	Name        string `yaml:"name"`
	URL         string `yaml:"url"`
	Description string `yaml:"description"`
	Icon        string `yaml:"icon"`
	Status      string `yaml:"-"`
}

type BookmarkGroup struct {
	Group string     `yaml:"group"`
	Items []Bookmark `yaml:"items"`
}

type Bookmark struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
	Abbr string `yaml:"abbr"`
}

type Settings struct {
	Title     string `yaml:"title"`
	Port      int    `yaml:"port"`       // Server port (default: 8080)
	ShowTitle bool   `yaml:"show_title"` // Show title in header (default: true)
	Favicon   string `yaml:"favicon"`    // Favicon URL or data URI (default: home emoji)
}

// SystemMetrics holds all system metrics collected for display
// in the user interface.
type SystemMetrics struct {
	CPULoad     float64
	MemoryUsed  float64
	MemoryTotal float64
	DiskUsed    float64
	DiskTotal   float64
}

// TemplateData holds all data passed to index.html template
type TemplateData struct {
	Config
	Version string
}

func loadTemplates() error {
	var err error

	// Template functions map
	templateFuncs := template.FuncMap{
		"getIconHTML":  getIconHTML,
		"getDomain":    getDomain,
		"getServiceID": getServiceID,
	}

	if useEmbedFS() {
		// Try to load from embedded FS
		templates, err = template.New("").Funcs(templateFuncs).ParseFS(templatesFS, "templates/*.html")
		if err != nil {
			return fmt.Errorf("error parsing embedded templates: %v", err)
		}
	} else {
		// Load from file system (dev mode)
		templates, err = template.New("").Funcs(templateFuncs).ParseGlob("templates/*.html")
		if err != nil {
			return fmt.Errorf("error parsing templates: %v", err)
		}
	}
	return nil
}

func loadConfig() error {
	configMutex.Lock()
	defer configMutex.Unlock()

	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("error reading config file: %v", err)
	}

	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return fmt.Errorf("error parsing config file: %v", err)
	}

	// Notify all clients that config has changed
	broadcastSSE(SSETypeReload, nil)

	return nil
}

type SSEMessageType string

const (
	SSETypeReload  SSEMessageType = "reload"
	SSETypeService SSEMessageType = "service"
	SSETypeMetrics SSEMessageType = "metrics"
)

type SSEMessage struct {
	Type SSEMessageType `json:"type"`
	Data any            `json:"data,omitempty"`
}

func broadcastSSE(msgType SSEMessageType, data any) {
	msg := SSEMessage{Type: msgType}

	if msgType != SSETypeReload {
		msg.Data = data
	}

	jsonMsg, err := json.Marshal(msg)
	if err != nil {
		return
	}
	message := string(jsonMsg)

	sseClientsMu.Lock()
	defer sseClientsMu.Unlock()

	for client := range sseClients {
		select {
		case client <- message:
		default:
			// Client not ready, skip
		}
	}
}

func watchConfig() error {
	absPath, err := filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("error getting absolute path: %v", err)
	}

	fmt.Printf("Starting config file watcher for: %s\n", absPath)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("error creating watcher: %v", err)
	}

	// Watch the directory to handle atomic writes (temp file + rename)
	dir := filepath.Dir(absPath)
	err = watcher.Add(dir)
	if err != nil {
		watcher.Close()
		return fmt.Errorf("error watching directory: %v", err)
	}

	// Start watching in background
	go func() {
		defer watcher.Close()
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// Check if the event is for our config file
				if filepath.Base(event.Name) == filepath.Base(absPath) &&
					event.Op&fsnotify.Write == fsnotify.Write {
					fmt.Printf("Config file modified: %s\n", event.Name)
					if err := loadConfig(); err != nil {
						fmt.Printf("Error reloading config: %v\n", err)
					} else {
						fmt.Println("Config reloaded successfully")
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				fmt.Printf("Watcher error: %v\n", err)
			}
		}
	}()

	return nil
}

func handleSSE(w http.ResponseWriter, r *http.Request) {
	// Set headers for SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Create a buffered channel for this client
	messageChan := make(chan string, 100)

	// Register this client
	sseClientsMu.Lock()
	sseClients[messageChan] = true
	sseClientsMu.Unlock()

	// Send current metrics to the new client immediately
	if metrics, err := collectSystemMetrics(); err == nil {
		msg := SSEMessage{Type: SSETypeMetrics, Data: metrics}
		jsonMsg, _ := json.Marshal(msg)
		select {
		case messageChan <- string(jsonMsg):
		default:
			// Client not ready, skip
		}
	}

	// Clean up when the client disconnects
	defer func() {
		sseClientsMu.Lock()
		delete(sseClients, messageChan)
		sseClientsMu.Unlock()
		close(messageChan)
	}()

	// Keep the connection alive
	for {
		select {
		case msg := <-messageChan:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			w.(http.Flusher).Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// checkServiceStatus performs a HEAD request to check if a service is responding.
func checkServiceStatus(url string) string {
	resp, err := httpClient.Head(url)
	// don't forget to close
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return "down"
	}
	return "up"
}

// getServiceStatus returns the current status from the background-updated store
func getServiceStatus(url string) string {
	serviceStatusMux.RLock()
	defer serviceStatusMux.RUnlock()

	if status, exists := serviceStatus[url]; exists {
		return status
	}
	return "checking"
}

// updateServiceStatusLoop runs in background and updates all service statuses periodically
func updateServiceStatusLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Initial check
	updateAllServiceStatus()

	for range ticker.C {
		updateAllServiceStatus()
	}
}

// updateAllServiceStatus checks all services in parallel and updates the status map
func updateAllServiceStatus() {
	configMutex.RLock()
	services := config.Services
	configMutex.RUnlock()

	var wg sync.WaitGroup
	tempStatus := make(map[string]string)
	var mu sync.Mutex

	// Check all services in parallel
	for _, group := range services {
		for _, service := range group.Items {
			wg.Add(1)
			go func(url string) {
				defer wg.Done()
				status := checkServiceStatus(url)

				mu.Lock()
				tempStatus[url] = status
				mu.Unlock()
			}(service.URL)
		}
	}

	wg.Wait()

	// First, read existing status with read lock
	serviceStatusMux.RLock()
	for url, newStatus := range tempStatus {
		if oldStatus, exists := serviceStatus[url]; !exists || oldStatus != newStatus {
			serviceID := getServiceID(url)
			serviceData := map[string]string{"id": serviceID, "status": newStatus}
			broadcastSSE(SSETypeService, serviceData)
		}
	}
	serviceStatusMux.RUnlock()

	// Then, update with write lock
	serviceStatusMux.Lock()
	serviceStatus = tempStatus
	serviceStatusMux.Unlock()
}

// updateMetricsLoop runs in background and broadcasts metrics updates periodically
func updateMetricsLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		metrics, err := collectSystemMetrics()
		if err != nil {
			continue
		}

		// Broadcast metrics update via SSE
		broadcastSSE(SSETypeMetrics, metrics)
	}
}

func getIconHTML(icon, name string) template.HTML {
	if icon == "" {
		return template.HTML(`<span class="iconify" data-icon="mdi:application" data-width="36" data-height="36"></span>`)
	}

	if strings.HasPrefix(icon, "mdi-") {
		return template.HTML(fmt.Sprintf(
			`<span class="iconify" data-icon="mdi:%s" data-width="36" data-height="36"></span>`,
			strings.TrimPrefix(icon, "mdi-"),
		))
	}

	if strings.HasPrefix(icon, "http://") || strings.HasPrefix(icon, "https://") {
		return template.HTML(fmt.Sprintf(`<img src='%s' alt='%s'>`, icon, name))
	}

	url := fmt.Sprintf("https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/png/%s", icon)
	return template.HTML(fmt.Sprintf(`<img src='%s' alt='%s'>`, url, name))
}

func getDomain(url string) string {
	// Remove protocol
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")

	// Get domain part (before first /)
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		domain := parts[0]
		// Remove www. prefix if present
		domain = strings.TrimPrefix(domain, "www.")
		return domain
	}
	return url
}

// collectSystemMetrics gathers real-time system metrics using gopsutil.
// Returns an error if collection fails.
func collectSystemMetrics() (SystemMetrics, error) {
	metrics := SystemMetrics{}

	// CPU usage - use 0 duration to get instant value (non-blocking)
	// Note: First call will return 0, subsequent calls show CPU usage since last call
	cpuPercent, err := cpu.Percent(0, false)
	if err != nil {
		return metrics, fmt.Errorf("failed to collect CPU metrics: %w", err)
	}
	if len(cpuPercent) > 0 {
		metrics.CPULoad = cpuPercent[0]
	}

	// Memory usage
	memory, err := mem.VirtualMemory()
	if err != nil {
		return metrics, fmt.Errorf("failed to collect memory metrics: %w", err)
	}
	metrics.MemoryUsed = float64(memory.Used) / 1024 / 1024 / 1024   // Convert to GB
	metrics.MemoryTotal = float64(memory.Total) / 1024 / 1024 / 1024 // Convert to GB

	// Disk usage (root partition)
	diskStat, err := disk.Usage("/")
	if err != nil {
		return metrics, fmt.Errorf("failed to collect disk metrics: %w", err)
	}
	metrics.DiskUsed = float64(diskStat.Used) / 1024 / 1024 / 1024   // Convert to GB
	metrics.DiskTotal = float64(diskStat.Total) / 1024 / 1024 / 1024 // Convert to GB

	return metrics, nil
}

// handleIndex serves the main index page with services, bookmarks, and metrics
func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	configMutex.RLock()

	// Get status for all services from background store
	for i := range config.Services {
		for j := range config.Services[i].Items {
			service := &config.Services[i].Items[j]
			service.Status = getServiceStatus(service.URL)
		}
	}

	data := struct {
		Config  Config
		Version string
	}{
		Config:  config,
		Version: Version,
	}
	configMutex.RUnlock()

	err := templates.ExecuteTemplate(w, "index.html", data)
	if err != nil {
		// Use fmt.Printf instead of http.Error to avoid WriteHeader conflicts
		fmt.Printf("Template execution error: %v\n", err)
		return
	}
}

func main() {
	// Parse command line flags
	flag.StringVar(&configPath, "config", "config.yaml", "Path to configuration file")
	flag.Parse()

	// Print version information
	fmt.Printf("Homepage Lite %s\n", Version)
	fmt.Printf("  Build Time: %s\n", BuildTime)
	fmt.Printf("  Git Commit: %s\n", GitCommit)
	fmt.Printf("  Go Version: %s\n", GoVersion)
	fmt.Printf("  Config: %s\n", configPath)
	fmt.Println()

	if err := loadConfig(); err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		return
	}

	if err := loadTemplates(); err != nil {
		fmt.Printf("Error loading templates: %v\n", err)
		return
	}

	if err := watchConfig(); err != nil {
		fmt.Printf("Error setting up config watcher: %v\n", err)
		return
	}

	// Start background goroutines
	go updateServiceStatusLoop()
	go updateMetricsLoop()

	// API routes
	http.HandleFunc("/events", handleSSE)

	// Static files - setup based on build mode
	setupStaticFiles()

	// Root route
	http.HandleFunc("/", handleIndex)

	// Get port from config or use default
	port := config.Settings.Port
	if port == 0 {
		port = 8080
	}

	addr := fmt.Sprintf(":%d", port)
	fmt.Printf("Starting server on %s\n", addr)
	err := http.ListenAndServe(addr, nil)
	if err != nil {
		fmt.Printf("Server error: %v\n", err)
	}
}
