package novis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

// ServiceStatus - Service status type
type ServiceStatus string

const (
	// UP - Service is up
	UP ServiceStatus = "UP"
	// DOWN - Service is down
	DOWN ServiceStatus = "DOWN"
	// PAUSE - Service is on pause
	PAUSE ServiceStatus = "PAUSE"
	// CHECKING - Service status is checking
	CHECKING ServiceStatus = "CHECKING"
)

const (
	svcKey = "_NOVIS_SVCS_"
)

// Service - Service Server
type Service struct {
	Host           string        `json:"host" yaml:"host"`
	Path           string        `json:"path" yaml:"path"`
	Status         ServiceStatus `json:"status"`
	HealthCheckURL string        `json:"healthCheckURL" yaml:"healthCheckURL"`
	reverseProxy   *httputil.ReverseProxy
	mux            *sync.RWMutex
}

// Novis - Service Proxy
type Novis struct {
	services map[string]*Service
	storage  *redis.Client
	server   *http.Server
	opts     *ProxyOptions
	mux      sync.RWMutex
}

// ProxyOptions - Service Proxy options
type ProxyOptions struct {
	Timeout      time.Duration
	DiscoveryURL string
	StorageOpts  *redis.Options
}

// Configuration - Configuration
type Configuration struct {
	Port   int16 `yaml:"port"`
	Server struct {
		Timeout   time.Duration `yaml:"timeout"`
		Discovery struct {
			Path string
		} `yaml:"discovery"`
		Services []Service `yaml:"services"`
	} `yaml:"server"`
}

// New - Create a new proxy
func New(port uint16, opts *ProxyOptions) *Novis {
	if opts == nil {
		opts = &ProxyOptions{
			Timeout:      10 * time.Second,
			DiscoveryURL: "discovery"}
	}
	n := &Novis{services: map[string]*Service{}, server: &http.Server{
		Addr: fmt.Sprintf(":%d", port)}, opts: opts, storage: redis.NewClient(opts.StorageOpts)}
	n.server.Handler = http.HandlerFunc(n.proxyRequest)
	n.LoadFromStorage()
	n.UpdateStorage()
	return n
}

// NewFromConfig - New Novis from Yaml config file
func NewFromConfig(fileName string, storageOpts *redis.Options) (nn *Novis, err error) {
	yc := Configuration{}
	yf, err := ioutil.ReadFile(fileName)
	if err != nil {
		return nn, err
	}
	err = yaml.Unmarshal(yf, &yc)
	if err != nil {
		return nn, err
	}
	initialServices := map[string]*Service{}
	for si := 0; si < len(yc.Server.Services); si++ {
		sURL, err := url.Parse(yc.Server.Services[si].GetHost())
		if err != nil {
			return nn, err
		}
		proxy := httputil.NewSingleHostReverseProxy(sURL)
		p := strings.Trim(yc.Server.Services[si].Path, "/")
		initialServices[p] = &Service{
			Host:           yc.Server.Services[si].GetHost(),
			Path:           yc.Server.Services[si].GetPath(),
			HealthCheckURL: yc.Server.Services[si].GetHealthCheckURL(),
			reverseProxy:   proxy,
			Status:         CHECKING}
	}
	nn = &Novis{
		services: initialServices,
		server: &http.Server{
			Addr: fmt.Sprintf(":%d", yc.Port)},
		opts: &ProxyOptions{
			Timeout:      yc.Server.Timeout * time.Second,
			DiscoveryURL: yc.Server.Discovery.Path,
			StorageOpts:  storageOpts},
		storage: redis.NewClient(storageOpts)}
	nn.server.Handler = http.HandlerFunc(nn.proxyRequest)
	err = nn.LoadFromStorage()
	nn.UpdateStorage()
	return nn, err
}

// LoadFromStorage - Load services from Storage
func (n *Novis) LoadFromStorage() (err error) {
	ctx := context.Background()
	res, err := n.storage.Get(ctx, svcKey).Result()
	if err != nil {
		if err == redis.Nil {
			return nil
		}
		return err
	}
	var services map[string]Service
	err = json.Unmarshal([]byte(res), &services)
	if err != nil {
		return err
	}
	for _, v := range services {
		sURL, err := url.Parse(v.GetHost())
		if err != nil {
			return err
		}
		proxy := httputil.NewSingleHostReverseProxy(sURL)
		n.AddService(&Service{
			Path:           v.GetPath(),
			Host:           v.GetHost(),
			HealthCheckURL: v.GetHealthCheckURL(),
			reverseProxy:   proxy,
			mux:            &sync.RWMutex{},
		})
	}
	return err
}

// Start - Start Server Proxy
func (n *Novis) Start(cb func(*Novis)) error {
	go n.healthCheck()
	if cb != nil {
		go cb(n)
	}
	err := n.server.ListenAndServe()
	return err
}

// healthCheck - Health check for services
func (n *Novis) healthCheck() {
	t := time.NewTicker(time.Second * 30)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			for _, s := range n.services {
				currentStatus := s.GetStatus()
				if currentStatus != PAUSE {
					sHost := s.GetHealthCheckURL()
					status := isServiceAlive(sHost)
					if currentStatus != status {
						s.SetStatus(status)
						err := n.UpdateStorage()
						if err != nil {
							log.Warn(err.Error())
						}
					}
				}
			}
		}
	}
}

// UpdateStorage - update to redis store
func (n *Novis) UpdateStorage() (err error) {
	ctx := context.Background()
	svcJSON, err := json.Marshal(n.services)
	if err != nil {
		return err
	}
	err = n.storage.Set(ctx, svcKey, string(svcJSON), 0).Err()
	return err
}

// AddService - Add service to map
func (n *Novis) AddService(service *Service) (err error) {
	p := strings.Trim(service.Path, "/")
	if len(p) > 0 {
		n.mux.Lock()
		n.services[strings.ToLower(p)] = service
		err = n.UpdateStorage()
		n.mux.Unlock()
		return nil
	}
	return nil
}

// PauseService - Pause a service
func (n *Novis) PauseService(service *Service) {
	p := strings.Split(service.Path, "/")
	if len(p) > 0 {
		n.services[strings.ToLower(p[0])].SetStatus(PAUSE)
	}
}

// ResumeService - Resume a service
func (n *Novis) ResumeService(service *Service) {
	p := strings.Split(service.Path, "/")
	if len(p) > 0 {
		n.services[strings.ToLower(p[0])].SetStatus(CHECKING)
	}
}

// RemoveService - Remove service from map
func (n *Novis) RemoveService(service *Service) (err error) {
	p := strings.Split(service.Path, "/")
	if len(p) > 0 {
		lp := strings.ToLower(p[0])
		n.mux.Lock()
		_, found := n.services[lp]
		if found {
			delete(n.services, lp)
			err = n.UpdateStorage()
		}
		n.mux.Unlock()
		return err
	}
	return err
}

// discovery - Discovery service
func (n *Novis) discovery(res http.ResponseWriter, req *http.Request) error {
	paths := removeEmptyStr(strings.Split(req.URL.Path, "/"))
	if len(paths) != 1 {
		Respond(res, http.StatusNotFound, nil, nil)
	} else if strings.ToLower(req.Method) != "post" {
		Respond(res, http.StatusMethodNotAllowed, nil, nil)
		return errors.New("Discovery request method not allowed")
	}
	defer req.Body.Close()
	var s Service
	err := json.NewDecoder(req.Body).Decode(&s)
	if err != nil {
		Respond(res, http.StatusBadRequest, nil, nil)
		return err
	}
	if len(s.HealthCheckURL) == 0 {
		Respond(res, http.StatusBadRequest, []byte("Health check url is not valid"), nil)
		return errors.New("Discovery request health check url is not valid")
	}
	s.mux = &sync.RWMutex{}
	s.SetStatus(CHECKING)
	sURL, err := url.Parse(s.GetHost())
	if err != nil {
		Respond(res, http.StatusBadRequest, nil, nil)
		return err
	}
	proxy := httputil.NewSingleHostReverseProxy(sURL)
	s.SetReverseProxy(proxy)
	n.AddService(&s)
	return nil
}

func (n *Novis) findServiceInMap(path string) string {
	for k := range n.services {
		if strings.HasPrefix(strings.ToLower(path), strings.ToLower(k)) {
			return k
		}
	}
	return ""
}

// proxyRequest - Proxy request to service
func (n *Novis) proxyRequest(res http.ResponseWriter, req *http.Request) {
	paths := strings.Trim(req.URL.Path, "/")
	if len(paths) > 0 {
		sn := n.findServiceInMap(paths)
		if paths == strings.ToLower(n.opts.DiscoveryURL) {
			_ = n.discovery(res, req)
		} else {
			n.mux.RLock()
			s, found := n.services[sn]
			n.mux.RUnlock()
			if found {
				if s.GetStatus() != UP {
					Respond(res, http.StatusNotFound, nil, nil)
					return
				}
				sURL, err := url.Parse(s.GetHost())
				if err != nil {
					log.WithField("URL", s.GetHost()).Warn(err.Error())
					Respond(res, http.StatusBadGateway, nil, nil)
					return
				}
				req.URL.Path = strings.Replace(req.URL.Path, "/"+s.GetPath(), "", 1)
				req.RequestURI = strings.Replace(req.RequestURI, "/"+s.GetPath(), "", 1)
				req.Host = sURL.Host
				s.GetReverseProxy().ServeHTTP(res, req)
				return
			}
			Respond(res, http.StatusNotFound, nil, nil)
		}
	} else {
		Respond(res, http.StatusOK, nil, nil)
	}
}

// GetAllServices - Get all available services
func (n *Novis) GetAllServices() (ss map[string]*Service) {
	n.mux.RLock()
	ss = n.services
	n.mux.RUnlock()
	return ss
}

// SetStatus - Set service status
func (s *Service) SetStatus(status ServiceStatus) {
	if s.mux != nil {
		s.mux.Lock()
		s.Status = status
		s.mux.Unlock()
	} else {
		s.Status = status
	}
}

// GetStatus - Get service status
func (s *Service) GetStatus() (alive ServiceStatus) {
	if s.mux != nil {
		s.mux.RLock()
		alive = s.Status
		s.mux.RUnlock()
	} else {
		alive = s.Status
	}
	return alive
}

// GetHost - Get Service URL
func (s *Service) GetHost() (u string) {
	if s.mux != nil {
		s.mux.RLock()
		u = s.Host
		s.mux.RUnlock()
	} else {
		u = s.Host
	}
	return u
}

// GetPath - Get path
func (s *Service) GetPath() (p string) {
	if s.mux != nil {
		s.mux.RLock()
		p = s.Path
		s.mux.RUnlock()
	} else {
		p = s.Path
	}
	return p
}

// SetReverseProxy - Set reverse proxy
func (s *Service) SetReverseProxy(rp *httputil.ReverseProxy) {
	if s.mux != nil {
		s.mux.Lock()
		s.reverseProxy = rp
		s.mux.Unlock()
	} else {
		s.reverseProxy = rp
	}
}

// GetReverseProxy - Get Reverse proxy
func (s *Service) GetReverseProxy() (rp *httputil.ReverseProxy) {
	if s.mux != nil {
		s.mux.RLock()
		rp = s.reverseProxy
		s.mux.RUnlock()
	} else {
		rp = s.reverseProxy
	}
	return rp
}

// SetHealthCheckURL - Set health check url
func (s *Service) SetHealthCheckURL(u string) {
	if s.mux != nil {
		s.mux.Lock()
		s.HealthCheckURL = u
		s.mux.Unlock()
	} else {
		s.HealthCheckURL = u
	}
}

// GetHealthCheckURL - Get Health check URL
func (s *Service) GetHealthCheckURL() (hcu string) {
	if s.mux != nil {
		s.mux.RLock()
		hcu = s.HealthCheckURL
		s.mux.RUnlock()
	} else {
		hcu = s.HealthCheckURL
	}
	return hcu
}

// Close - Close down server
func (n *Novis) Close() (err error) {
	err = n.server.Close()
	if err != nil {
		return err
	}
	err = n.storage.Close()
	return err
}

// isServiceAlive - Check if a service is available to connect
func isServiceAlive(u string) ServiceStatus {
	to := 3 * time.Second
	conn, err := net.DialTimeout("tcp", u, to)
	if err != nil {
		return DOWN
	}
	_ = conn.Close()
	return UP
}

// Respond - Respond back to client
func Respond(res http.ResponseWriter, status int, msg []byte, headers http.Header) error {
	for k, v := range headers {
		res.Header()[k] = v
	}
	res.WriteHeader(status)
	_, err := res.Write(msg)
	return err
}
