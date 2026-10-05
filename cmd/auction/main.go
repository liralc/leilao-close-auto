package main

import (
	"context"
	"leilao-close-auto/configuration/database/mongodb"
	"log"

	"github.com/joho/godotenv"
)

func main() {
	ctx := context.Background()

	if err := godotenv.Load("cmd/auction/.env"); err != nil {
		log.Fatal("Error trying to load env variables")
		return
	}

	// databaseClient, err := mongodb.NewMongoDBConnection(ctx)
	_, err := mongodb.NewMongoDBConnection(ctx)
	if err != nil {
		log.Fatal("Error trying to connect to mongodb database", err)
		return
	}
}
