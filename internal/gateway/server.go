// server.go holds the Server value and New. Login is session.go, the HTTP
// front door is engine.go, route mounting is routes.go and ingress.go, spend
// is spend.go, and the Host adapters are wire.go.

package gateway

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sunqirui1987/xhub/internal/authz"
	"github.com/sunqirui1987/xhub/internal/catalog"
	"github.com/sunqirui1987/xhub/internal/config"
	dpcontract "github.com/sunqirui1987/xhub/internal/dataplane/contract"
	"github.com/sunqirui1987/xhub/internal/gateway/affinity"
	"github.com/sunqirui1987/xhub/internal/gateway/family"
	"github.com/sunqirui1987/xhub/internal/gateway/models"
	"github.com/sunqirui1987/xhub/internal/iam"
	"github.com/sunqirui1987/xhub/internal/live"
	_ "github.com/sunqirui1987/xhub/internal/provider/all"
	"github.com/sunqirui1987/xhub/internal/store"
	"github.com/sunqirui1987/xhub/internal/util"
)

const Version = family.ProxyVersion

// Server is one gateway process. Fields are grouped by who writes them.
// Request-scoped maps below are keyed by call id and deleted when the spend
// row is written, so a handler must AnnotateCall and RememberExchange before
// RecordSpend.
type Server struct {
	Cfg   *config.Config
	Store *store.Store
	// IAM is users, teams, organizations, projects, keys, and the audit log.
	// Authz is the only authorization check over that store.
	IAM   *iam.DB
	Authz *authz.Authorizer
	// Client is the upstream HTTP client. Its timeout is the router timeout.
	Client *http.Client
	// enterpriseClient 仅供隔离协议测试注入 TLS 身份服务；生产 nil 时强制公网安全客户端。
	enterpriseClient *http.Client
	// engine is the Gin mux. registered remembers "METHOD pattern" so a catalog
	// route cannot replace a module route that was mounted first.
	engine     *gin.Engine
	registered map[string]struct{}
	Cache      *util.DualCache
	// Busy counts in-flight calls per deployment id inside this process.
	Busy map[string]int
	// Live is Redis. Nil means spend is written to PostgreSQL inside the request
	// and affinity pins stay in the map below.
	Live *live.Client
	// rpmHits and tpmHits are the in-process rate windows used when Redis is off.
	rpmHits map[string][]time.Time
	tpmHits map[string][]tokHit
	// catalog is the routes.json table mounted after the modules.
	catalog []catalog.Route
	// sessions is the console login state. idem is the authenticated
	// Idempotency-Key replay and in-flight table.
	sessions map[string]sessionRec
	idem     map[string]*idemRec
	idemTTL  time.Duration
	now      func() time.Time
	mu       sync.Mutex
	// yamlStoreModelInDB is the startup flag. A database override can change
	// the same behavior later through prefs.
	yamlStoreModelInDB bool
	modules            []util.Module
	modulesReady       bool
	// exchanges holds headers and bodies until recordSpend copies them onto the
	// usage row. callNotes holds provider, TTFT, session, and deployment.
	// guardrailNotes holds the rule name when a guardrail refused the call.
	exchanges      map[string]promptExchange
	callNotes      map[string]dpcontract.CallNote
	guardrailNotes map[string]string
	// State 独立管理会话与任务归属；通过组合提供数据面需要的路由接口。
	affinity.State
}

type idemRec struct {
	RequestHash [32]byte
	ExpiresAt   time.Time
	Done        chan struct{}
	Code        int
	CT          string
	Body        []byte
	Hdr         map[string]string
}

const defaultIdempotencyTTL = 24 * time.Hour

type tokHit struct {
	t time.Time
	n int
}

// ValidateKeyRelations is gone with the old store: a key's ownership is
// resolved from the database by the authorization layer on every request, so
// there is no separate validation step that could disagree with it.

var logTraceOnceServer sync.Once

// New assembles the gateway. It loads the catalog and registers dedicated routes plus the remaining catalog routes. The identity store is required: a gateway without one could not tell who is calling.
// 调用：authz/authz.go、authz/decide.go、cache/util.go、dataplane/execution/serve.go
// 测试：activity_http_test.go、authz_test.go、builtin_providers_test.go
// 参数 cfg（*config.Config）：进程配置，含模型表和路由策略；st（*store.Store）：此刻的冷却、延迟、用量和并发，用来排序；db（*iam.DB）：身份和用量库。
// 返回 *Server（*Server）：装好模块和目录路由的网关进程。
func New(cfg *config.Config, st *store.Store, db *iam.DB) *Server {
	logTraceOnceServer.Do(func() { util.Trace("enter gateway.New") })

	s := &Server{
		Cfg:                cfg,
		Store:              st,
		IAM:                db,
		Authz:              authz.New(db),
		Client:             &http.Client{},
		engine:             newEngine(),
		registered:         map[string]struct{}{},
		Cache:              util.NewCache(),
		Busy:               map[string]int{},
		rpmHits:            map[string][]time.Time{},
		tpmHits:            map[string][]tokHit{},
		catalog:            catalog.Load(),
		sessions:           map[string]sessionRec{},
		idem:               map[string]*idemRec{},
		idemTTL:            defaultIdempotencyTTL,
		now:                time.Now,
		yamlStoreModelInDB: cfg.GeneralSettings.StoreModelInDB,
	}
	if cfg.GeneralSettings.RedisURL != "" {
		if client, err := live.Open(cfg.GeneralSettings.RedisURL); err == nil {
			s.Live = client
		}
	}
	if s.Live != nil {
		s.State = *affinity.New(s.Live)
	}
	models.LoadStored(s)
	// Hand-entered price rows and suppliers are laid over the embedded price
	// catalog before any request is served.
	models.LoadPriceOverrides(s)
	// An armed reload plan keeps the market prices current without a restart.
	models.StartScheduledReload(s)
	s.seedAdmin()
	s.installModules()
	s.mountModules()
	// mountCatalog registers each catalog route on Gin by itself. Unregistered paths are not swallowed by a "/" handler that would hide 404s.
	s.mountCatalog()
	return s
}

// Run 绑定 addr 并启动 HTTP 服务及依赖身份库的维护任务，供进程入口调用。
// 参数 addr 为监听地址，空串按 net.Listen 的默认规则处理；返回监听或服务退出错误。
// 监听失败不启动后台任务；服务退出取消维护，PostgreSQL-only 部署同样维护预算周期。
// 测试：organization_maintenance_test.go 验证成功监听后的空闲组织维护与退出清理。
func (s *Server) Run(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.serveListener(ln)
}

// serveListener 在成功监听后启动维护任务并提供 HTTP 服务；ln 为 Run 或隔离测试创建的监听器。
// 返回服务退出错误；退出取消预算/归档任务，IAM 存在时即启动维护，不以 Redis 可用性为条件。
func (s *Server) serveListener(ln net.Listener) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// 监听成功且有身份库时启动预算维护及可选 Redis 刷写；纯 PostgreSQL 也必须重置空闲组织预算。
	// 服务退出时取消，避免启动失败留下后台任务。
	if s.IAM != nil {
		go s.flushLoop(ctx)
	}
	go s.archiveLoop(ctx)
	return (&http.Server{Handler: s.Handler()}).Serve(ln)
}
