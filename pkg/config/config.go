package config

import (
	"os"
)

type Config struct {
	DBDialect   string
	DBAddress   string
	HTTPPort    string
	RabbitMQURL string
	RedisURL    string
	WebhookURL  string
}

func Load() *Config {
	return &Config{
		DBDialect:   getEnv("DB_DIALECT", "postgres"),
		DBAddress:   getEnv("DB_ADDRESS", "postgres://user:pass@localhost/dbname?sslmode=disable"),
		HTTPPort:    getEnv("HTTP_PORT", "8080"),
		RabbitMQURL: getEnv("RABBITMQ_URL", "amqp://whatsmeow:securepassword@localhost:5675/"),
		RedisURL:    getEnv("REDIS_URL", "localhost:16379"),
		WebhookURL:  getEnv("WEBHOOK_URL", ""),
	}
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}
