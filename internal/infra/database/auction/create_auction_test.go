package auction

import (
	"context"
	"leilao-close-auto/internal/entity/auction_entity"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestGetAuctionDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration string
		interval string
		want     time.Duration
	}{
		{name: "uses AUCTION_DURATION", duration: "30s", interval: "1m", want: 30 * time.Second},
		{name: "falls back to AUCTION_INTERVAL", duration: "", interval: "1m", want: time.Minute},
		{name: "invalid value falls back to default", duration: "abc", interval: "", want: defaultAuctionDuration},
		{name: "non positive value falls back to default", duration: "-10s", interval: "0s", want: defaultAuctionDuration},
		{name: "unset uses default", want: defaultAuctionDuration},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(AuctionDurationEnv, tt.duration)
			t.Setenv(legacyAuctionIntervalEnv, tt.interval)

			if got := GetAuctionDuration(); got != tt.want {
				t.Errorf("GetAuctionDuration() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCloseAuctionWhenExpired valida, sem MongoDB real, que após o prazo é enviado
// um update que altera somente leilões ativos para Completed.
func TestCloseAuctionWhenExpired(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))

	mt.Run("sends update to close active auction", func(mt *mtest.T) {
		repo := &AuctionRepository{Collection: mt.Coll, auctionDuration: 50 * time.Millisecond}
		mt.AddMockResponses(mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}, bson.E{Key: "nModified", Value: 1}))

		auctionId := uuid.NewString()
		start := time.Now()
		repo.closeAuctionWhenExpired(auctionId, start.Add(repo.auctionDuration))

		if elapsed := time.Since(start); elapsed < repo.auctionDuration {
			mt.Fatalf("auction closed after %v, before configured duration %v", elapsed, repo.auctionDuration)
		}

		event := mt.GetStartedEvent()
		if event == nil || event.CommandName != "update" {
			mt.Fatalf("expected update command, got %+v", event)
		}

		update := event.Command.Lookup("updates").Array().Index(0).Value().Document()
		filter := update.Lookup("q").Document()
		if got := filter.Lookup("_id").StringValue(); got != auctionId {
			mt.Errorf("filter _id = %q, want %q", got, auctionId)
		}
		if got := filter.Lookup("status").AsInt64(); got != int64(auction_entity.Active) {
			mt.Errorf("filter status = %d, want %d (Active)", got, auction_entity.Active)
		}
		set := update.Lookup("u", "$set").Document()
		if got := set.Lookup("status").AsInt64(); got != int64(auction_entity.Completed) {
			mt.Errorf("$set status = %d, want %d (Completed)", got, auction_entity.Completed)
		}
	})

	mt.Run("returns error when update fails", func(mt *mtest.T) {
		repo := &AuctionRepository{Collection: mt.Coll}
		mt.AddMockResponses(mtest.CreateCommandErrorResponse(mtest.CommandError{Code: 1, Message: "boom"}))

		if err := repo.closeAuction(context.Background(), uuid.NewString()); err == nil {
			mt.Fatal("expected error, got nil")
		}
	})
}

// TestCreateAuction_ClosesAutomatically é um teste de integração com MongoDB real.
// Executa apenas quando MONGODB_URL está definida (ver README).
func TestCreateAuction_ClosesAutomatically(t *testing.T) {
	mongoURL := os.Getenv("MONGODB_URL")
	if mongoURL == "" {
		t.Skip("MONGODB_URL not set; skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURL))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	if err := client.Ping(ctx, nil); err != nil {
		t.Fatalf("ping: %v", err)
	}

	database := client.Database("auctions_test_" + uuid.NewString()[:8])
	t.Cleanup(func() { _ = database.Drop(context.Background()) })

	const auctionDuration = 2 * time.Second
	t.Setenv(AuctionDurationEnv, auctionDuration.String())
	repo := NewAuctionRepository(database)

	// Passo 1: criar leilão e verificar que está aberto.
	auctionEntity, internalErr := auction_entity.CreateAuction(
		"Notebook", "Eletronicos", "Notebook usado em bom estado", auction_entity.Used)
	if internalErr != nil {
		t.Fatalf("create entity: %v", internalErr)
	}
	if internalErr := repo.CreateAuction(ctx, auctionEntity); internalErr != nil {
		t.Fatalf("create auction: %v", internalErr)
	}

	created, internalErr := repo.FindAuctionById(ctx, auctionEntity.Id)
	if internalErr != nil {
		t.Fatalf("find auction: %v", internalErr)
	}
	if created.Status != auction_entity.Active {
		t.Fatalf("status after creation = %d, want Active (%d)", created.Status, auction_entity.Active)
	}

	// Passo 2: aguardar o tempo configurado em AUCTION_DURATION.
	time.Sleep(auctionDuration)

	// Passo 3: verificar que o status foi alterado para fechado sem intervenção manual.
	// Uma pequena margem cobre o agendamento da goroutine e a latência do banco.
	deadline := time.Now().Add(3 * time.Second)
	for {
		closed, internalErr := repo.FindAuctionById(ctx, auctionEntity.Id)
		if internalErr != nil {
			t.Fatalf("find auction: %v", internalErr)
		}
		if closed.Status == auction_entity.Completed {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("auction status = %d, want Completed (%d)", closed.Status, auction_entity.Completed)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
