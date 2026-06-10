package daemon

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/BrandonYaniz/yllmd/internal/config"
	"github.com/BrandonYaniz/yllmd/internal/models"
	"github.com/BrandonYaniz/yllmd/internal/protocol"
	"github.com/BrandonYaniz/yllmd/internal/providers"
	"github.com/BrandonYaniz/yllmd/internal/storage"
)

type Server struct {
	cfg      config.Config
	models   models.Registry
	provider providers.Provider
	logger   *slog.Logger

	listener *net.UnixListener
	jobs     chan *generateJob
	done     chan struct{}

	mu       sync.Mutex
	active   map[string]context.CancelFunc
	queued   map[string]*generateJob
	shutdown bool
}

type generateJob struct {
	request protocol.Request
	client  *clientConn
	ctx     context.Context
	cancel  context.CancelFunc
}

type clientConn struct {
	conn net.Conn
	mu   sync.Mutex
}

func NewServer(cfg config.Config, provider providers.Provider, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		cfg:      cfg,
		models:   models.NewRegistry(cfg),
		provider: provider,
		logger:   logger,
		jobs:     make(chan *generateJob, cfg.Queue.MaxDepth),
		done:     make(chan struct{}),
		active:   make(map[string]context.CancelFunc),
		queued:   make(map[string]*generateJob),
	}
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	if err := prepareSocket(s.cfg.Server.SocketPath); err != nil {
		return err
	}
	addr := net.UnixAddr{Name: s.cfg.Server.SocketPath, Net: "unix"}
	ln, err := net.ListenUnix("unix", &addr)
	if err != nil {
		return err
	}
	s.listener = ln
	if err := chmodSocket(s.cfg.Server.SocketPath, s.cfg.Server.SocketMode); err != nil {
		_ = ln.Close()
		return err
	}
	if err := chgrpSocket(s.cfg.Server.SocketPath, s.cfg.Server.SocketGroup); err != nil {
		_ = ln.Close()
		return err
	}
	defer func() {
		if closeable, ok := s.provider.(providers.Closeable); ok {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := closeable.Close(shutdownCtx); err != nil {
				s.logger.Debug("provider close failed", "error", err)
			}
		}
		_ = ln.Close()
		_ = os.Remove(s.cfg.Server.SocketPath)
	}()

	go s.worker()
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		s.shutdown = true
		for _, cancel := range s.active {
			cancel()
		}
		for _, job := range s.queued {
			job.cancel()
		}
		s.mu.Unlock()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				close(s.done)
				return nil
			default:
				return err
			}
		}
		go s.handleConn(&clientConn{conn: conn})
	}
}

func (s *Server) handleConn(client *clientConn) {
	defer client.conn.Close()
	scanner := bufio.NewScanner(client.conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		req, err := protocol.DecodeRequest(scanner.Bytes())
		if err != nil {
			_ = client.write(protocol.Event{Type: "error", Code: "invalid_request", Message: err.Error()})
			continue
		}
		s.handleRequest(client, req)
	}
	if err := scanner.Err(); err != nil {
		s.logger.Debug("client read failed", "error", err)
	}
}

func (s *Server) handleRequest(client *clientConn, req protocol.Request) {
	switch req.Type {
	case protocol.MessageHealth:
		status := s.daemonStatus()
		_ = client.write(protocol.Event{Type: "health", ID: req.ID, Status: status.Status, Daemon: &status, LoadedModel: status.LoadedModel, QueueDepth: status.QueueDepth})
	case protocol.MessageStatus:
		status := s.daemonStatus()
		_ = client.write(protocol.Event{Type: "status", ID: req.ID, Status: status.Status, Daemon: &status, LoadedModel: status.LoadedModel, QueueDepth: status.QueueDepth})
	case protocol.MessageGenerate:
		s.enqueueGenerate(client, req)
	case protocol.MessageCancel:
		if s.cancel(req.ID) {
			_ = client.write(protocol.Event{Type: "cancelled", ID: req.ID})
		} else {
			_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "request_not_active", Message: "no queued or active request matched the id"})
		}
	case protocol.MessageProviders:
		_ = client.write(protocol.Event{Type: "providers", ID: req.ID, Provider: "local"})
	case protocol.MessageModels:
		s.handleModels(client, req)
	default:
		_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "unknown_request", Message: fmt.Sprintf("unsupported request type %q", req.Type)})
	}
}

func (s *Server) handleModels(client *clientConn, req protocol.Request) {
	switch req.Action {
	case "", "list":
		_ = client.write(protocol.Event{Type: "models", ID: req.ID, Models: s.modelDescriptors()})
	case "install":
		s.installModel(client, req)
	case "activate":
		s.activateModel(client, req)
	case "versions":
		s.listModelVersions(client, req)
	case "rollback":
		s.rollbackModel(client, req)
	default:
		_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "unknown_models_action", Message: fmt.Sprintf("unsupported models action %q", req.Action)})
	}
}

func (s *Server) installModel(client *clientConn, req protocol.Request) {
	if err := req.ValidateModelInstall(); err != nil {
		_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "invalid_request", Message: err.Error()})
		return
	}
	model, err := s.models.Resolve(req.Model)
	if err != nil {
		_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "model_unavailable", Message: err.Error()})
		return
	}
	activate := true
	if req.Activate != nil {
		activate = *req.Activate
	}
	if activate && !s.isIdle() {
		_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "daemon_busy", Message: "model activation requires an idle daemon"})
		return
	}
	result, err := storage.NewModelStore(s.cfg).InstallLocalFile(storage.InstallRequest{
		ModelName:  model.Name,
		VersionID:  req.Version,
		SourcePath: req.File,
		SHA256:     req.SHA256,
		CatalogID:  model.Config.CatalogID,
		Activate:   activate,
	})
	if err != nil {
		_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "install_failed", Message: err.Error()})
		return
	}
	if activate {
		s.reloadProvider()
	}
	_ = client.write(protocol.Event{Type: "installed", ID: req.ID, Model: result.ModelName, Version: result.VersionID, Path: result.ModelPath})
}

func (s *Server) activateModel(client *clientConn, req protocol.Request) {
	if err := req.ValidateModelActivate(); err != nil {
		_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "invalid_request", Message: err.Error()})
		return
	}
	model, err := s.models.Resolve(req.Model)
	if err != nil {
		_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "model_unavailable", Message: err.Error()})
		return
	}
	if !s.isIdle() {
		_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "daemon_busy", Message: "model activation requires an idle daemon"})
		return
	}
	activation, err := storage.NewModelStore(s.cfg).ActivateVersion(model.Name, req.Version)
	if err != nil {
		_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "activate_failed", Message: err.Error()})
		return
	}
	s.reloadProvider()
	_ = client.write(protocol.Event{
		Type:    "activated",
		ID:      req.ID,
		Model:   activation.ModelName,
		Version: activation.VersionID,
		Path:    storage.NewModelStore(s.cfg).CurrentModelPath(activation.ModelName),
	})
}

func (s *Server) listModelVersions(client *clientConn, req protocol.Request) {
	if err := req.ValidateModelVersions(); err != nil {
		_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "invalid_request", Message: err.Error()})
		return
	}
	model, err := s.models.Resolve(req.Model)
	if err != nil {
		_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "model_unavailable", Message: err.Error()})
		return
	}
	versions, err := storage.NewModelStore(s.cfg).ListVersions(model.Name)
	if err != nil {
		_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "versions_failed", Message: err.Error()})
		return
	}
	_ = client.write(protocol.Event{Type: "versions", ID: req.ID, Model: model.Name, Versions: modelVersions(versions)})
}

func (s *Server) rollbackModel(client *clientConn, req protocol.Request) {
	if err := req.ValidateModelRollback(); err != nil {
		_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "invalid_request", Message: err.Error()})
		return
	}
	model, err := s.models.Resolve(req.Model)
	if err != nil {
		_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "model_unavailable", Message: err.Error()})
		return
	}
	if !s.isIdle() {
		_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "daemon_busy", Message: "model rollback requires an idle daemon"})
		return
	}
	result, err := storage.NewModelStore(s.cfg).RollbackLatest(model.Name)
	if err != nil {
		_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "rollback_failed", Message: err.Error()})
		return
	}
	s.reloadProvider()
	_ = client.write(protocol.Event{Type: "rolled_back", ID: req.ID, Model: result.ModelName, Version: result.VersionID, Path: storage.NewModelStore(s.cfg).CurrentModelPath(result.ModelName)})
}

func (s *Server) reloadProvider() {
	closeable, ok := s.provider.(providers.Closeable)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := closeable.Close(ctx); err != nil {
		s.logger.Debug("provider reload close failed", "error", err)
	}
}

func (s *Server) isIdle() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.active) == 0 && len(s.queued) == 0
}

func (s *Server) enqueueGenerate(client *clientConn, req protocol.Request) {
	if err := req.ValidateGenerate(); err != nil {
		_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "invalid_request", Message: err.Error()})
		return
	}
	if req.Provider == "" || req.Provider == "auto" {
		req.Provider = s.cfg.Routing.DefaultProvider
	}
	if req.Provider != "local" {
		_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "provider_not_implemented", Message: "remote providers are skeletoned but not implemented in v1"})
		return
	}
	if req.Model == "" {
		req.Model = s.cfg.ModelLifecycle.ResidentModel
	}
	if _, err := s.models.Resolve(req.Model); err != nil {
		_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "model_unavailable", Message: err.Error()})
		return
	}
	timeout := s.requestTimeout(req)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	job := &generateJob{request: req, client: client, ctx: ctx, cancel: cancel}

	s.mu.Lock()
	if s.shutdown {
		s.mu.Unlock()
		cancel()
		_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "shutting_down", Message: "daemon is shutting down"})
		return
	}
	if _, exists := s.active[req.ID]; exists {
		s.mu.Unlock()
		cancel()
		_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "duplicate_request", Message: "request id is already active"})
		return
	}
	if _, exists := s.queued[req.ID]; exists {
		s.mu.Unlock()
		cancel()
		_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "duplicate_request", Message: "request id is already queued"})
		return
	}
	position := len(s.jobs) + 1
	s.queued[req.ID] = job
	s.mu.Unlock()

	select {
	case s.jobs <- job:
		_ = client.write(protocol.Event{Type: "accepted", ID: req.ID, QueuePosition: position})
	case <-ctx.Done():
		s.removeQueued(req.ID)
		_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "queue_timeout", Message: "request timed out before entering queue"})
	default:
		s.removeQueued(req.ID)
		cancel()
		_ = client.write(protocol.Event{Type: "error", ID: req.ID, Code: "queue_full", Message: "request queue is full"})
	}
}

func (s *Server) worker() {
	for job := range s.jobs {
		s.removeQueued(job.request.ID)
		select {
		case <-job.ctx.Done():
			_ = job.client.write(protocol.Event{Type: "error", ID: job.request.ID, Code: "cancelled", Message: "request cancelled before start"})
			continue
		default:
		}
		s.setActive(job.request.ID, job.cancel)
		s.runJob(job)
		s.clearActive(job.request.ID)
		job.cancel()
	}
}

func (s *Server) runJob(job *generateJob) {
	stream := true
	if job.request.Stream != nil {
		stream = *job.request.Stream
	}
	events, err := s.provider.Generate(job.ctx, providers.GenerateRequest{
		ID:       job.request.ID,
		Provider: "local",
		Model:    job.request.Model,
		Stream:   stream,
		Input:    *job.request.Input,
		Settings: job.request.Settings,
	})
	if err != nil {
		_ = job.client.write(protocol.Event{Type: "error", ID: job.request.ID, Code: "provider_failed", Message: err.Error()})
		return
	}
	for {
		select {
		case <-job.ctx.Done():
			_ = job.client.write(protocol.Event{Type: "error", ID: job.request.ID, Code: "cancelled", Message: "request cancelled"})
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if err := job.client.write(event); err != nil {
				job.cancel()
				return
			}
		}
	}
}

func (s *Server) cancel(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel, ok := s.active[id]; ok {
		cancel()
		return true
	}
	if job, ok := s.queued[id]; ok {
		job.cancel()
		delete(s.queued, id)
		return true
	}
	return false
}

func (s *Server) setActive(id string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[id] = cancel
}

func (s *Server) clearActive(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, id)
}

func (s *Server) removeQueued(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.queued, id)
}

func (s *Server) queueDepth() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queued)
}

func (s *Server) loadedModel() string {
	if model, err := s.models.Resident(); err == nil {
		return model.Name
	}
	descriptors := s.models.Descriptors()
	if len(descriptors) > 0 {
		return descriptors[0].Name
	}
	return ""
}

func (s *Server) daemonStatus() protocol.DaemonStatus {
	return protocol.DaemonStatus{
		Status:        "ok",
		Provider:      s.cfg.Routing.DefaultProvider,
		LoadedModel:   s.loadedModel(),
		QueueDepth:    s.queueDepth(),
		ModelCount:    len(s.models.Descriptors()),
		RemoteEnabled: s.remoteEnabled(),
	}
}

func (s *Server) modelDescriptors() []protocol.ModelDescriptor {
	descriptors := s.models.Descriptors()
	store := storage.NewModelStore(s.cfg)
	for i := range descriptors {
		activeVersion, err := store.ActiveVersion(descriptors[i].Name)
		if err != nil {
			continue
		}
		if descriptors[i].ProviderMetadata == nil {
			descriptors[i].ProviderMetadata = make(map[string]string)
		}
		descriptors[i].ProviderMetadata["active_version"] = activeVersion
	}
	return descriptors
}

func modelVersions(versions []storage.VersionInfo) []protocol.ModelVersion {
	descriptors := make([]protocol.ModelVersion, 0, len(versions))
	for _, version := range versions {
		descriptor := protocol.ModelVersion{
			Version:      version.VersionID,
			Active:       version.Active,
			Path:         version.ModelPath,
			ManifestPath: version.ManifestPath,
			ChecksumPath: version.ChecksumPath,
		}
		if version.Manifest != nil {
			descriptor.SHA256 = version.Manifest.SHA256
			if !version.Manifest.InstalledAt.IsZero() {
				descriptor.InstalledAt = version.Manifest.InstalledAt.Format(time.RFC3339)
			}
		}
		descriptors = append(descriptors, descriptor)
	}
	return descriptors
}

func (s *Server) remoteEnabled() bool {
	for _, provider := range s.cfg.RemoteProviders {
		if provider.Enabled {
			return true
		}
	}
	return false
}

func (s *Server) requestTimeout(req protocol.Request) time.Duration {
	if req.Queue.TimeoutMS > 0 {
		return time.Duration(req.Queue.TimeoutMS) * time.Millisecond
	}
	return s.cfg.Queue.DefaultTimeout
}

func (c *clientConn) write(event protocol.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return protocol.WriteEvent(c.conn, event)
}

func prepareSocket(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		conn, dialErr := net.DialTimeout("unix", path, 100*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return fmt.Errorf("socket %s is already in use", path)
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func chmodSocket(path, modeText string) error {
	if modeText == "" {
		return nil
	}
	mode, err := strconv.ParseUint(modeText, 8, 32)
	if err != nil {
		return fmt.Errorf("invalid socket mode %q: %w", modeText, err)
	}
	return os.Chmod(path, os.FileMode(mode))
}

func chgrpSocket(path, groupName string) error {
	if groupName == "" {
		return nil
	}
	group, err := user.LookupGroup(groupName)
	if err != nil {
		return fmt.Errorf("lookup socket group %q: %w", groupName, err)
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return fmt.Errorf("invalid gid %q for socket group %q: %w", group.Gid, groupName, err)
	}
	return os.Chown(path, -1, gid)
}
