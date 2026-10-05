# Leilão com fechamento automático

Sistema de leilões em Go (Gin + MongoDB) baseado em
[labs-auction-goexpert](https://github.com/devfullcycle/labs-auction-goexpert), com
**fechamento automático** de leilões: ao criar um leilão, uma goroutine é agendada e, quando
o tempo configurado expira, o status do leilão é alterado no banco para fechado
(`Completed`, valor `1`).

## Como funciona

A implementação está em `internal/infra/database/auction/create_auction.go`:

1. `CreateAuction` insere o leilão (status `Active`, valor `0`).
2. Em seguida dispara `go closeAuctionWhenExpired(id, timestamp + AUCTION_DURATION)`, sem bloquear a requisição.
3. A goroutine espera com um `time.Timer` até o fim do leilão e executa um `UpdateOne`
   com filtro `{_id, status: Active}` e `$set: {status: Completed}`, com contexto próprio
   e timeout de 10s. O filtro por status torna a operação idempotente.

A validação de lances (`internal/infra/database/bid/create_bid.go`) usa a **mesma** duração
(`auction.GetAuctionDuration()`), então lances são recusados exatamente no mesmo prazo.

## Variáveis de ambiente

Definidas em `cmd/auction/.env`:

| Variável                | Descrição                                                                          | Padrão |
|-------------------------|------------------------------------------------------------------------------------|--------|
| `AUCTION_DURATION`      | Duração do leilão até o fechamento automático. Formato `time.ParseDuration` (`30s`, `5m`, `1h`). | `5m` |
| `AUCTION_INTERVAL`      | Nome legado do projeto base. Usada apenas se `AUCTION_DURATION` não estiver definida/válida. | — |
| `BATCH_INSERT_INTERVAL` | Intervalo máximo para gravar o lote de lances.                                     | `3m`   |
| `MAX_BATCH_SIZE`        | Tamanho máximo do lote de lances.                                                  | `5`    |
| `MONGODB_URL`           | URL de conexão com o MongoDB.                                                      | —      |
| `MONGODB_DB`            | Nome do banco.                                                                     | —      |

Valores ausentes, inválidos ou não positivos de duração resultam no padrão de 5 minutos.

Para alterar o tempo, edite `AUCTION_DURATION` em `cmd/auction/.env` (ex: `AUCTION_DURATION=1m`)
e recrie o container (`docker compose up -d --build`). Variáveis definidas no ambiente
têm precedência sobre o arquivo `.env`.

## Rodando com Docker Compose

```bash
docker compose up -d --build
```

A API sobe em `http://localhost:8080` e o MongoDB em `localhost:27017`.

### Testando manualmente

```bash
# Criar leilão (condition: 0=Novo, 1=Usado, 2=Recondicionado)
curl -i -X POST http://localhost:8080/auction \
  -H 'Content-Type: application/json' \
  -d '{"product_name":"Notebook","category":"Eletronicos","description":"Notebook usado em bom estado","condition":1}'

# Listar leilões ativos (status=0)
curl 'http://localhost:8080/auction?status=0'

# Após AUCTION_DURATION (20s por padrão), o leilão aparece como fechado (status=1)
curl 'http://localhost:8080/auction?status=1'
```

Nos logs da aplicação (`docker compose logs app`) aparece a mensagem
`Auction closed automatically` com o `auction_id`.

## Testes

```bash
# Testes unitários (não precisam de MongoDB; o teste de integração é ignorado)
go test -race ./...

# Teste de integração do fechamento automático, com MongoDB real
docker compose up -d mongodb
MONGODB_URL='mongodb://admin:admin@localhost:27017/?authSource=admin' \
  go test -race -count=1 -v ./internal/infra/database/auction/
```

O teste `TestCreateAuction_ClosesAutomatically` segue o roteiro do desafio:

1. cria um leilão e verifica status `Active`;
2. aguarda o tempo de `AUCTION_DURATION` (2s no teste);
3. verifica que o status mudou para `Completed` sem intervenção manual.

O teste usa um banco temporário (`auctions_test_<id>`) que é removido ao final.
