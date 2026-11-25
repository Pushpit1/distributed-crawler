package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

func main() {
	log.Println("Starting system connectivity check...")

	// 1. Test Redis Connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	redisClient := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "", // No password set in docker-compose
		DB:       0,  // Default DB
	})

	log.Println("Connecting to Redis at localhost:6379...")
	redisPing, err := redisClient.Ping(ctx).Result()
	if err != nil {
		log.Fatalf("❌ Failed to connect to Redis: %v", err)
	}
	fmt.Printf("✅ Redis connected successfully! Ping response: %s\n", redisPing)
	redisClient.Close()

	// 2. Test PostgreSQL Connection
	// Connection string format: postgres://user:password@host:port/dbname?sslmode=disable
	connStr := "postgres://crawler_user:crawler_password@localhost:5433/crawler_db?sslmode=disable"
	log.Println("Connecting to PostgreSQL at localhost:5433...")

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("❌ Failed to initialize PostgreSQL driver: %v", err)
	}
	defer db.Close()

	// sql.Open doesn't verify the connection immediately. We must call Ping() to test it.
	dbCtx, dbCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dbCancel()

	err = db.PingContext(dbCtx)
	if err != nil {
		log.Fatalf("❌ Failed to ping PostgreSQL database: %v", err)
	}
	fmt.Println("✅ PostgreSQL connected successfully!")

	log.Println("🎉 All systems are green and ready for development!")
}
