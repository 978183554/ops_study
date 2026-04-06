package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Prometheus 指标
var (
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"path", "method", "status"},
	)

	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Duration of HTTP requests",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"path", "method"},
	)
)

// 全局数据库连接
var db *sql.DB

// 商品结构
type Product struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Price int    `json:"price"`
	Stock int    `json:"stock"`
}

// 初始化数据库
func initDB() {
	dbHost := os.Getenv("DB_HOST")
	if dbHost == "" {
		dbHost = "localhost"
	}
	dbPort := os.Getenv("DB_PORT")
	if dbPort == "" {
		dbPort = "3306"
	}
	dbUser := os.Getenv("DB_USER")
	if dbUser == "" {
		dbUser = "root"
	}
	dbPassword := os.Getenv("DB_PASSWORD")
	dbName := os.Getenv("DB_NAME")
	if dbName == "" {
		dbName = "shopping"
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s", dbUser, dbPassword, dbHost, dbPort, dbName)
	var err error
	db, err = sql.Open("mysql", dsn)
	if err != nil {
		log.Printf("ERROR: 数据库连接失败: %v", err)
		log.Println("INFO: 使用内存模式运行...")
		return
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)

	// 创建表
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS products (
		id INT PRIMARY KEY AUTO_INCREMENT,
		name VARCHAR(100),
		price INT,
		stock INT
	);
	`
	db.Exec(createTableSQL)

	// 初始化数据
	db.Exec("INSERT IGNORE INTO products (id, name, price, stock) VALUES (1, 'iPhone 15', 5999, 100)")
	db.Exec("INSERT IGNORE INTO products (id, name, price, stock) VALUES (2, 'MacBook Pro', 12999, 50)")
	db.Exec("INSERT IGNORE INTO products (id, name, price, stock) VALUES (3, 'iPad Air', 4999, 75)")

	log.Println("INFO: 数据库已连接")
}

// 记录指标
func recordMetrics(path, method, status string) {
	httpRequestsTotal.WithLabelValues(path, method, status).Inc()
}

// 处理请求的中间件
func withMetrics(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		defer func() {
			duration := time.Since(start).Seconds()
			httpRequestDuration.WithLabelValues(r.URL.Path, r.Method).Observe(duration)
		}()
		handler(w, r)
	}
}

// 首页
func homeHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	html := `
	<h1>🛍️ 云威商城系统</h1>
	<h2>API列表:</h2>
	<ul>
		<li><a href="/products">/products</a> - 商品列表</li>
		<li><a href="/products/1">/products/1</a> - 商品详情</li>
		<li><a href="/health">/health</a> - 健康检查</li>
		<li><a href="/config">/config</a> - 配置信息</li>
		<li><a href="/metrics">/metrics</a> - Prometheus指标</li>
	</ul>
	`
	w.Write([]byte(html))
	recordMetrics("/", r.Method, "200")
}

// 获取所有商品
func getProductsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if db == nil {
		// 内存模式：返回示例数据
		products := []Product{
			{ID: 1, Name: "iPhone 15", Price: 5999, Stock: 100},
			{ID: 2, Name: "MacBook Pro", Price: 12999, Stock: 50},
			{ID: 3, Name: "iPad Air", Price: 4999, Stock: 75},
		}
		json.NewEncoder(w).Encode(products)
		recordMetrics("/products", r.Method, "200")
		return
	}

	rows, err := db.Query("SELECT id, name, price, stock FROM products")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		recordMetrics("/products", r.Method, "500")
		return
	}
	defer rows.Close()

	var products []Product
	for rows.Next() {
		var p Product
		rows.Scan(&p.ID, &p.Name, &p.Price, &p.Stock)
		products = append(products, p)
	}

	json.NewEncoder(w).Encode(products)
	recordMetrics("/products", r.Method, "200")
}

// 获取单个商品
func getProductHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	id := r.URL.Query().Get("id")
	if id == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "缺少id参数"})
		recordMetrics("/products", r.Method, "400")
		return
	}

	if db == nil {
		// 内存模式
		if id == "1" {
			json.NewEncoder(w).Encode(Product{ID: 1, Name: "iPhone 15", Price: 5999, Stock: 100})
		}
		recordMetrics("/products", r.Method, "200")
		return
	}

	var p Product
	err := db.QueryRow("SELECT id, name, price, stock FROM products WHERE id = ?", id).
		Scan(&p.ID, &p.Name, &p.Price, &p.Stock)

	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "商品不存在"})
		recordMetrics("/products", r.Method, "404")
		return
	}

	json.NewEncoder(w).Encode(p)
	recordMetrics("/products", r.Method, "200")
}

// 健康检查
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status := "UP"
	code := http.StatusOK

	if db != nil {
		if err := db.Ping(); err != nil {
			status = "DOWN"
			code = http.StatusServiceUnavailable
		}
	}

	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"status": status})
	recordMetrics("/health", r.Method, fmt.Sprintf("%d", code))
}

// 配置信息
func configHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	config := map[string]string{
		"db_host":     os.Getenv("DB_HOST"),
		"db_port":     os.Getenv("DB_PORT"),
		"redis_host":  os.Getenv("REDIS_HOST"),
		"redis_port":  os.Getenv("REDIS_PORT"),
		"app_version": "1.0.0",
		"environment": "kubernetes",
	}

	json.NewEncoder(w).Encode(config)
	recordMetrics("/config", r.Method, "200")
}

func main() {
	// 初始化数据库
	initDB()
	defer func() {
		if db != nil {
			db.Close()
			log.Println("INFO: 数据库连接已关闭")
		}
	}()

	// 注册路由
	mux := http.NewServeMux()
	mux.HandleFunc("/", withMetrics(homeHandler))
	mux.HandleFunc("/products", withMetrics(func(w http.ResponseWriter, r *http.Request) {
		if id := r.URL.Query().Get("id"); id != "" {
			getProductHandler(w, r)
		} else {
			getProductsHandler(w, r)
		}
	}))
	mux.HandleFunc("/health", withMetrics(healthHandler))
	mux.HandleFunc("/config", withMetrics(configHandler))
	mux.Handle("/metrics", promhttp.Handler())

	// 创建 HTTP 服务器
	server := &http.Server{
		Addr:         ":8080",
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// 启动服务器的 goroutine
	serverErrors := make(chan error, 1)
	go func() {
		log.Println("INFO: 🚀 商城系统在 :8080 启动...")
		serverErrors <- server.ListenAndServe()
	}()

	// 监听关闭信号 (SIGTERM, SIGINT)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	// 组织关闭流程

	shutdownDone := make(chan struct{})

	go func() {
		sig := <-sigChan
		log.Printf("INFO: 收到信号 %v，开始优雅关闭...", sig)

		// 创建超时上下文，30秒内完成关闭
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// 关闭服务器，等待现有请求完成
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("ERROR: 服务器关闭出错: %v", err)
		}
		close(shutdownDone)
	}()

	// 等待服务器启动错误或优雅关闭完成
	select {
	case err := <-serverErrors:
		if err != nil && err != http.ErrServerClosed {
			log.Printf("ERROR: 服务器启动失败: %v", err)
			os.Exit(1)
		}
	case <-shutdownDone:
		log.Println("INFO: 服务器已优雅关闭")
	}
}
