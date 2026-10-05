package auction

import (
	"context"
	"fmt"
	"leilao-close-auto/configuration/logger"
	"leilao-close-auto/internal/entity/auction_entity"
	"leilao-close-auto/internal/internal_error"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"
)

const (
	// AuctionDurationEnv define por quanto tempo um leilão permanece aberto (ex: "20s", "5m").
	AuctionDurationEnv = "AUCTION_DURATION"
	// legacyAuctionIntervalEnv é a variável usada originalmente pelo projeto base; mantida como fallback.
	legacyAuctionIntervalEnv = "AUCTION_INTERVAL"

	defaultAuctionDuration = 5 * time.Minute
	closeAuctionTimeout    = 10 * time.Second
)

type AuctionEntityMongo struct {
	Id          string                          `bson:"_id"`
	ProductName string                          `bson:"product_name"`
	Category    string                          `bson:"category"`
	Description string                          `bson:"description"`
	Condition   auction_entity.ProductCondition `bson:"condition"`
	Status      auction_entity.AuctionStatus    `bson:"status"`
	Timestamp   int64                           `bson:"timestamp"`
}
type AuctionRepository struct {
	Collection      *mongo.Collection
	auctionDuration time.Duration
}

func NewAuctionRepository(database *mongo.Database) *AuctionRepository {
	return &AuctionRepository{
		Collection:      database.Collection("auctions"),
		auctionDuration: GetAuctionDuration(),
	}
}

// GetAuctionDuration lê a duração do leilão de AUCTION_DURATION (com fallback para
// AUCTION_INTERVAL). Valores ausentes, inválidos ou não positivos resultam no padrão de 5 minutos.
func GetAuctionDuration() time.Duration {
	for _, key := range []string{AuctionDurationEnv, legacyAuctionIntervalEnv} {
		duration, err := time.ParseDuration(os.Getenv(key))
		if err == nil && duration > 0 {
			return duration
		}
	}

	return defaultAuctionDuration
}

func (ar *AuctionRepository) CreateAuction(
	ctx context.Context,
	auctionEntity *auction_entity.Auction) *internal_error.InternalError {
	auctionEntityMongo := &AuctionEntityMongo{
		Id:          auctionEntity.Id,
		ProductName: auctionEntity.ProductName,
		Category:    auctionEntity.Category,
		Description: auctionEntity.Description,
		Condition:   auctionEntity.Condition,
		Status:      auctionEntity.Status,
		Timestamp:   auctionEntity.Timestamp.Unix(),
	}
	_, err := ar.Collection.InsertOne(ctx, auctionEntityMongo)
	if err != nil {
		logger.Error("Error trying to insert auction", err)
		return internal_error.NewInternalServerError("Error trying to insert auction")
	}

	// O fechamento roda em background para não bloquear a requisição de criação.
	go ar.closeAuctionWhenExpired(auctionEntity.Id, auctionEntity.Timestamp.Add(ar.auctionDuration))

	return nil
}

// closeAuctionWhenExpired aguarda até endTime e então marca o leilão como fechado.
// Usa um contexto próprio, pois o contexto da requisição de criação já terá terminado.
func (ar *AuctionRepository) closeAuctionWhenExpired(auctionId string, endTime time.Time) {
	timer := time.NewTimer(time.Until(endTime))
	defer timer.Stop()
	<-timer.C

	ctx, cancel := context.WithTimeout(context.Background(), closeAuctionTimeout)
	defer cancel()

	if err := ar.closeAuction(ctx, auctionId); err != nil {
		logger.Error(fmt.Sprintf("Error trying to close auction id = %s", auctionId), err)
		return
	}

	logger.Info("Auction closed automatically", zap.String("auction_id", auctionId))
}

// closeAuction altera o status para Completed apenas se o leilão ainda estiver ativo,
// tornando a operação idempotente.
func (ar *AuctionRepository) closeAuction(ctx context.Context, auctionId string) error {
	filter := bson.M{"_id": auctionId, "status": auction_entity.Active}
	update := bson.M{"$set": bson.M{"status": auction_entity.Completed}}

	if _, err := ar.Collection.UpdateOne(ctx, filter, update); err != nil {
		return err
	}

	return nil
}
