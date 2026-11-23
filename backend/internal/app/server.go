package app

import (
	"context"
	"database/sql"
	"errors"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/unrealm-ai/kikoteam/backend/internal/config"
	managercontroller "github.com/unrealm-ai/kikoteam/backend/internal/manager/controller"
	grouphistory "github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/grouphistory"
	humanconfirmation "github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/humanconfirmation"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/inputattachments"
	delegation "github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/taskdelegation"
	taskspacehistory "github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/taskspacehistory"
	workitems "github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/workitems"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration/channelprobe"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration/dingtalksetup"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration/feishugroups"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration/feishusetup"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration/skillasset"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration/wecomcontacts"
	mpcontroller "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/controller"
	mpintegration "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/integration"
	mprepo "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/repository"
	mpservice "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/service"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/repository"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"

	"strings"
	"syscall"
	"time"
)

func Run(stderr io.Writer, version string) int {
	log := slog.New(slog.NewJSONHandler(stderr, nil)).With("service", "backend", "version", version)
	cfg, err := config.Load()
	if err != nil {
		log.Error("Invalid configuration", "event", "startup.invalid_config", "error_code", "invalid_config", "reason", err.Error())
		return 1
	}
	origin := strings.TrimRight(cfg.Server.Origin, "/")
	u, e := url.Parse(origin)
	secret := cfg.AuthSecret
	dsn := cfg.DSN
	if e != nil || !validOrigin(u, cfg.AllowHTTPIPOrigin) || len(secret) < 32 || dsn == "" {
		log.Error("Set DATABASE_URL, KIKO_PUBLIC_ORIGIN and KIKO_AUTH_SECRET (32+ bytes)", "event", "startup.invalid_config", "error_code", "invalid_config")
		return 1
	}
	proxyOrigin, err := resolveModelProxyOrigin(origin, cfg.ModelProxyOrigin)
	if err != nil {
		log.Error(err.Error(), "event", "startup.invalid_config", "error_code", "invalid_config")
		return 1
	}
	db, e := sql.Open("pgx", dsn)
	if e != nil {
		log.Error("Invalid PostgreSQL configuration", "event", "startup.invalid_database", "error_code", "invalid_config")
		return 1
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Error("Database close failed", "event", "shutdown.database_failed", "error_code", "close_failed")
		}
	}()
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	fallbackMailPolicy := service.MailPolicy{Enabled: cfg.Mail.Enabled, VerificationRequired: cfg.Auth.RequireEmailVerification}
	authStore := &repository.AuthStore{DB: db, InitCredits: mprepo.Signup, MailFallback: &fallbackMailPolicy}
	mailSettings := &service.MailSettingsService{
		Store: &repository.MailSettingsStore{DB: db}, Secret: []byte(secret), AllowTest: authStore.Allow,
		Fallback: service.MailSettingsDraft{Password: cfg.SMTPPassword, MailSettings: service.MailSettings{
			MailPolicy: fallbackMailPolicy,
			Address:    cfg.Mail.SMTP.Address, Host: cfg.Mail.SMTP.Host, Username: cfg.Mail.SMTP.Username,
			From: cfg.Mail.SMTP.From, ImplicitTLS: cfg.Mail.SMTP.ImplicitTLS,
		}},
		SendTest: func(ctx context.Context, d service.MailSettingsDraft, to string) error {
			return integration.SMTPForSettings(d).SendTest(ctx, to)
		},
	}
	mailer := integration.RuntimeSMTP{Resolve: mailSettings.Runtime}
	var proxies []netip.Prefix
	if raw := cfg.Server.TrustedProxies; raw != "" {
		for _, cidr := range strings.Split(raw, ",") {
			p, err := netip.ParsePrefix(strings.TrimSpace(cidr))
			if err != nil {
				log.Error("Invalid trusted proxy CIDR", "event", "startup.invalid_config", "error_code", "invalid_proxy")
				return 1
			}
			proxies = append(proxies, p)
		}
	}
	proxySecrets := mpservice.Secrets{Root: []byte(secret)}
	proxyStore := mprepo.Store{DB: db}
	proxyUpstream := mpintegration.Upstream{HTTP: mpintegration.Client()}
	official := &mpservice.Models{Store: proxyStore, Secrets: proxySecrets, Upstream: proxyUpstream}
	auth := &service.AuthService{Store: authStore, Mailer: mailer, Secret: []byte(secret), ReadMailPolicy: mailSettings.Policy}
	ws := &service.WorkspaceService{Store: repository.NewWorkspaceStore(db), PublicURL: origin, ReadMailPolicy: mailSettings.Policy, MailSecret: []byte(secret)}
	models := &service.ModelService{OnReleaseError: func() {
		log.Warn("Model probe lease cleanup failed; will expire", "event", "model.probe_cleanup_failed", "error_code", "lease_cleanup_failed")
	}, Store: repository.NewModelStore(db), Prober: &integration.ModelProber{}, NormalizeEndpoint: integration.NormalizeModelEndpoint, Secret: []byte(secret)}

	assetConfig, err := skillasset.LoadConfig(os.Getenv)
	if err != nil {
		log.Error("Invalid asset storage configuration", "event", "startup.invalid_storage", "error_code", "invalid_config")
		return 1
	}
	if err := manager.UpgradeProjectUploadReceipts(assetConfig.LocalRoot); err != nil {
		log.Error("Project upload receipt upgrade failed", "event", "startup.project_upload_upgrade_failed", "error", err)
		return 1
	}
	assets, err := skillasset.NewStore(context.Background(), assetConfig)
	if err != nil {
		log.Error("Asset storage initialization failed", "event", "startup.storage_failed", "error_code", "storage_unavailable")
		return 1
	}
	defer func() {
		if closeErr := assets.Close(); closeErr != nil {
			log.Error("Asset storage close failed", "event", "shutdown.storage_failed", "error_code", "close_failed")
		}
	}()
	skills := &service.SkillService{Store: repository.NewSkillStore(db), Assets: assets}
	channelStore := repository.NewChannelStore(db)
	channels := &service.ChannelService{Store: channelStore, Secret: []byte(secret), CredentialProbe: &channelprobe.Client{}}
	wecomContacts := &service.WecomContactsService{Channels: channels, Store: &repository.WecomContactStore{DB: db}, Provider: &wecomcontacts.Client{}}
	feishuSetup := &service.FeishuSetupService{Store: &repository.FeishuSetupStore{DB: db}, Channels: channels, Provider: &feishusetup.Client{}}
	dingtalkSetup := &service.DingTalkSetupService{
		Store: &repository.DingTalkSetupStore{DB: db}, Channels: channels, Provider: &dingtalksetup.Client{},
		Source: strings.TrimSpace(os.Getenv("KIKO_DINGTALK_REGISTRATION_SOURCE")),
	}
	hubToken, hubErr := loadHubToken()
	if hubErr != nil {
		log.Error("Invalid Hub token", "event", "startup.invalid_config")
		return 1
	}
	oauthApps := &service.OAuthAppService{Store: &repository.OAuthAppStore{DB: db}, Secret: []byte(secret),
		LegacyClientID: cfg.GitHubOAuthClientID, LegacyClientSecret: cfg.GitHubOAuthClientSecret,
		RedirectURI: origin + "/api/v1/auth/callbacks/plugin-oauth"}
	plugins := &service.PluginService{
		OAuthApps:   oauthApps,
		RedirectURI: origin + "/api/v1/auth/callbacks/plugin-oauth", Store: repository.NewPluginStore(db),
		Secret: []byte(secret), Probe: &integration.MCPProber{PrivateCIDRs: cfg.MCPPrivateCIDRs},
		GitHubClientID: cfg.GitHubOAuthClientID, GitHubClientSecret: cfg.GitHubOAuthClientSecret,
	}
	packages := &service.PackageService{Store: repository.NewPackageStore(db)}
	templates := &service.TemplateService{Store: &repository.TemplateStore{DB: db, EnsureKey: proxySecrets.EnsureKey}, Assets: assets}
	admins := &service.AdminService{Store: &repository.AdminStore{DB: db}}
	wecomGroupStore := &repository.WecomGroupRefreshStore{DB: db}
	api := (&controller.Server{Catalog: &repository.ResourceCatalog{DB: db, Assets: assets, Plugins: plugins}, SharingPolicies: &service.GroupSharingService{Store: &repository.GroupSharingStore{DB: db}}, PeopleSelectors: &service.PeopleSelectorService{Secret: []byte(secret), Channels: channels, Wecom: wecomContacts, Store: repository.NewPeopleSelectorStore(db, []byte(secret))}, GroupSelectors: &service.GroupSelectorService{Store: repository.NewGroupSelectorStore(db), Secret: []byte(secret)}, WecomGroups: &service.WecomGroupRefresh{Channels: channels, Store: wecomGroupStore}, ChannelGroups: &service.ChannelGroupDiscovery{Channels: channels, Store: &repository.ChannelGroupCache{DB: db}, Provider: &feishugroups.Client{}}, AgentUsage: &service.AgentUsageService{Store: &repository.AgentUsageStore{DB: db}}, WebGroups: &service.WebGroupService{Store: &repository.WebGroupStore{DB: db}}, WecomContacts: wecomContacts, Official: official, Templates: templates, Admin: admins, Channels: channels, FeishuSetup: feishuSetup, DingTalkSetup: dingtalkSetup, Skills: skills, Plugins: plugins, Packages: packages, Agents: &service.AgentService{Store: &repository.AgentStore{DB: db, EnsureKey: proxySecrets.EnsureKey}}, Models: models, Auth: auth, Workspaces: ws, Origin: origin, SecureCookies: u.Scheme == "https", Logger: log, TrustedProxies: proxies, MailSettings: mailSettings, OAuthApps: oauthApps, ChannelDownloads: &service.ChannelDownloadSettingsService{Store: &repository.ChannelDownloadSettingsStore{DB: db}}}).Handler()
	mux := http.NewServeMux()
	controller.ChannelInternalRoutes(mux, channels, hubToken)
	controller.WecomGroupRefreshInternalRoutes(mux, wecomGroupStore, hubToken)
	(mpcontroller.Proxy{Calls: proxyStore, Secrets: proxySecrets, Upstream: proxyUpstream, Log: log}).Routes(mux, hubToken)
	managerStore := &managerrepo.Store{DB: db, WorkAssets: assets, ChannelLog: log, RuntimeRoot: assetConfig.LocalRoot, TriggerSecret: []byte(secret), ConsumeWecomControl: repository.ConsumeWecomControl([]byte(secret)), SnapshotCapabilities: repository.SnapshotCapabilities}
	managerService := &manager.Service{ProjectRoot: assetConfig.LocalRoot, Store: managerStore, CursorSecret: []byte(secret)}
	channelServer := &managercontroller.ChannelServer{Store: managerStore, Manager: managerService, Token: hubToken, Logger: log}
	channelServer.Routes(mux)
	channelServer.TriggerNotificationRoutes(mux)
	engineToken, err := loadRuntimeToken()
	if err != nil {
		log.Error("KIKO_ENGINE_TOKEN must have at least 32 bytes", "event", "startup.invalid_config")
		return 1
	}
	(&managercontroller.Server{TrustedProxies: proxies, Runtime: &service.RuntimeService{Packages: packages, Skills: skills, Plugins: plugins}, Service: managerService, Engines: managerStore, EngineToken: engineToken, ResolveModelKey: func(ctx context.Context, w, actor string, c manager.Configuration) (string, error) {
		return models.RuntimeModelKey(ctx, w, actor, c.ModelProfileID, c.Protocol, c.BaseURL, c.ModelID)
	}, ResolveOfficialModel: func(ctx context.Context, w, actor, agent string, c *manager.Configuration) (string, error) {
		if err := models.AuthorizeOfficialRuntime(ctx, w, actor, agent); err != nil {
			return "", err
		}
		c.BaseURL = proxyOrigin + "/model-proxy"
		if c.Protocol != "anthropic_messages" {
			c.BaseURL += "/v1"
		}
		c.ModelID = c.PlatformModelID
		return proxySecrets.RuntimeKey(ctx, db, w, agent, c.PlatformModelID, c.Protocol)
	}, Authenticate: func(ctx context.Context, token string) (string, error) {
		account, err := auth.Me(ctx, token)
		return account.ID, err
	}, Origin: origin, Logger: log}).Routes(mux)
	err = registerManagerMCP(mux, managerStore, managerService, secret, engineToken, log)
	if err != nil {
		log.Error("Manager MCP contract invalid", "event", "startup.invalid_contract")
		return 1
	}
	mux.Handle("/api/", api)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	static := cfg.Server.WebDir
	if static != "" {
		mux.Handle("/", webFiles(static))
	}
	addr := cfg.Server.Address
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	server := &http.Server{Addr: addr, Handler: noReferrer(mux), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 75 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	catalog, err := service.BuiltinCatalog()
	if err != nil {
		log.Error("Invalid official template catalog", "reason", err.Error())
		return 1
	}
	stopCatalog := startTemplateCatalog(ctx, db, catalog, templates, log)
	defer stopCatalog()
	channelCtx, cancelChannel := context.WithCancel(ctx)
	channelDone := make(chan struct{})
	go func() { defer close(channelDone); channelServer.Run(channelCtx) }()
	defer func() { cancelChannel(); <-channelDone }()
	notificationCtx, cancelNotification := context.WithCancel(ctx)
	notificationDone := make(chan struct{})
	go func() { defer close(notificationDone); channelServer.RunTriggerNotifications(notificationCtx) }()
	defer func() { cancelNotification(); <-notificationDone }()
	stopWebGroups := startWebGroupWorker(ctx, managerStore, log)
	defer stopWebGroups()
	stopFeishuRegistration := startFeishuSetup(ctx, feishuSetup, log)
	defer stopFeishuRegistration()
	stopDingTalkRegistration := startDingTalkSetup(ctx, dingtalkSetup, log)
	defer stopDingTalkRegistration()
	stopDirectory := startChannelDirectory(ctx, channels, log)
	defer stopDirectory()
	stopChannelNotifications := startChannelNotifications(ctx, channelStore, hubToken, log)
	defer stopChannelNotifications()
	stopDispatch := startRuntimeDispatch(ctx, managerStore, log, engineToken)
	defer stopDispatch()
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		log.Error("HTTP listener failed", "event", "server.failed", "error_code", "listen_failed")
		return 1
	}
	stopAssetCleanup := startAssetCleanup(ctx, skills.Cleanup, log)
	defer stopAssetCleanup()
	deliveryCtx, cancelDelivery := context.WithCancel(ctx)
	deliveryDone := make(chan struct{})
	{
		dispatcher := &service.DeliveryDispatcher{Store: repository.NewDeliveryStore(db), Workspaces: ws, Sender: mailer, Secret: []byte(secret), ReadMailPolicy: mailSettings.Policy, OnError: func() {
			log.Error("Invitation dispatch attempt failed", "event", "invitation.delivery_failed", "error_code", "dispatch_failed")
		}}
		go func() {
			defer close(deliveryDone)
			if runErr := dispatcher.Run(deliveryCtx); runErr != nil && !errors.Is(runErr, context.Canceled) {
				log.Error("Invitation dispatcher stopped", "event", "invitation.dispatcher_stopped", "error_code", "dispatch_failed")
			}
		}()
	}
	defer func() {
		cancelDelivery()
		select {
		case <-deliveryDone:
		case <-time.After(15 * time.Second):
			log.Error("Invitation dispatcher shutdown timed out", "event", "shutdown.delivery_timeout", "error_code", "shutdown_timeout")
		}
	}()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	log.Info("Backend listening", "event", "startup.ready", "address", addr)
	select {
	case e = <-done:
		if !errors.Is(e, http.ErrServerClosed) {
			log.Error("HTTP listener failed", "event", "server.failed", "error_code", "listen_failed")
			return 1
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if e = server.Shutdown(shutdown); e != nil {
			log.Error("HTTP shutdown failed", "event", "shutdown.failed", "error_code", "shutdown_timeout")
			return 1
		}
	}
	return 0
}
func validOrigin(u *url.URL, allowHTTPIP bool) bool {
	if !validHTTPOrigin(u) {
		return false
	}
	host := u.Hostname()
	return u.Scheme == "https" || host == "localhost" || host == "127.0.0.1" || (allowHTTPIP && net.ParseIP(host) != nil)
}

func validHTTPOrigin(u *url.URL) bool {
	return u.Host != "" && (u.Scheme == "https" || u.Scheme == "http") && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && u.User == nil
}

func resolveModelProxyOrigin(publicOrigin, configured string) (string, error) {
	if configured == "" {
		return publicOrigin, nil
	}
	origin := strings.TrimRight(configured, "/")
	u, err := url.Parse(origin)
	if err != nil || !validHTTPOrigin(u) {
		return "", errors.New("invalid KIKO_MODEL_PROXY_ORIGIN")
	}
	return origin, nil
}

func registerManagerMCP(mux *http.ServeMux, store *managerrepo.Store, mgr *manager.Service, secret, engineToken string, log *slog.Logger) error {
	handler, err := delegation.Handler(&manager.Delegation{Manager: mgr, Store: store, Secret: engineToken}, log)
	if err != nil {
		return err
	}
	mux.Handle("/mcp/task-delegation", handler)
	inputattachments.Register(mux, store, mgr, engineToken)
	history, err := grouphistory.Handler(&manager.GroupHistoryRuntime{Store: store, Secret: engineToken}, log)
	if err != nil {
		return err
	}
	mux.Handle("/mcp/group-history", history)
	if err := taskspacehistory.Register(mux, &manager.TaskSpaceHistory{Store: store, CursorSecret: []byte(secret)}, engineToken, log); err != nil {
		return err
	}
	if err := humanconfirmation.Register(mux, store, engineToken); err != nil {
		return err
	}
	return workitems.Register(mux, store, engineToken)
}
func webFiles(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/webhooks/") || strings.HasPrefix(r.URL.Path, "/mcp/") {
			http.NotFound(w, r)
			return
		}
		path := filepath.Join(dir, filepath.Clean("/"+r.URL.Path))
		if st, e := os.Stat(path); e == nil && !st.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		if filepath.Ext(r.URL.Path) != "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	})
}

func noReferrer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
