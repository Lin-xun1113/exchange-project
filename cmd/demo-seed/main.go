// Command demo-seed populates a fresh demo stack and exits.
package main

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"os/signal"
	"syscall"
	"time"

	sqldriver "github.com/go-sql-driver/mysql"
	orderpb "github.com/linxun2025/exchange-project/api/gen/order/v1"
	userpb "github.com/linxun2025/exchange-project/api/gen/user/v1"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/pkg/config"
	"github.com/linxun2025/exchange-project/pkg/grpcx"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

const (
	rpcTimeout  = 5 * time.Second
	password    = "demo1234"
	firstTrader = 2
	traderCount = 12
	demoAccount = firstTrader + traderCount
)

type seedAccount struct {
	name      string
	id        int64
	available map[string]decimal.Decimal
	reserve   map[string]decimal.Decimal
}

type accounts [demoAccount + 1]seedAccount

func newAccounts() accounts {
	var users accounts
	users[0] = seedAccount{name: "mm_north", available: assetAmounts(50000000, 1000, 20000)}
	users[1] = seedAccount{name: "mm_south", available: assetAmounts(50000000, 1000, 20000)}
	for i := range traderCount {
		users[firstTrader+i] = seedAccount{name: fmt.Sprintf("trader%02d", i+1), available: assetAmounts(200000, 3, 60)}
	}
	users[demoAccount] = seedAccount{
		name: "demo", available: assetAmounts(100000, 2, 30), reserve: assetAmounts(50000, 1, 10),
	}
	return users
}

func assetAmounts(usdt, btc, eth int64) map[string]decimal.Decimal {
	return map[string]decimal.Decimal{
		quoteAsset: decimal.NewFromInt(usdt), "BTC": decimal.NewFromInt(btc), "ETH": decimal.NewFromInt(eth),
	}
}

func (a *seedAccount) canSpend(asset string, amount, reserve decimal.Decimal) bool {
	return a.available[asset].GreaterThanOrEqual(amount.Add(reserve))
}

func (a *accounts) counterparties(rng *rand.Rand, base string, notional, qty decimal.Decimal) (buyer, seller *seedAccount, err error) {
	if rng.Float64() < 0.7 {
		buyer, seller = &a[rng.IntN(firstTrader)], &a[firstTrader+rng.IntN(traderCount)]
		if rng.IntN(2) == 0 {
			buyer, seller = seller, buyer
		}
	} else {
		b, s := rng.IntN(traderCount), rng.IntN(traderCount-1)
		if s >= b {
			s++
		}
		buyer, seller = &a[firstTrader+b], &a[firstTrader+s]
	}
	if rng.IntN(60) == 0 {
		if rng.IntN(2) == 0 {
			buyer = &a[demoAccount]
		} else {
			seller = &a[demoAccount]
		}
	}
	if !buyer.canSpend(quoteAsset, notional, buyer.reserve[quoteAsset]) {
		buyer = a.fundedMaker(seller, quoteAsset, notional)
		if buyer == nil {
			return nil, nil, fmt.Errorf("no distinct market maker can buy %s USDT", notional)
		}
	}
	if !seller.canSpend(base, qty, seller.reserve[base]) {
		seller = a.fundedMaker(buyer, base, qty)
		if seller == nil {
			return nil, nil, fmt.Errorf("no distinct market maker can sell %s %s", qty, base)
		}
	}
	return buyer, seller, nil
}

func (a *accounts) fundedMaker(other *seedAccount, asset string, amount decimal.Decimal) *seedAccount {
	for i := range firstTrader {
		maker := &a[i]
		if maker != other && maker.canSpend(asset, amount, decimal.Zero) {
			return maker
		}
	}
	return nil
}

func (a *accounts) createAndFund(ctx context.Context, user userpb.UserServiceClient) error {
	for i := range a {
		account := &a[i]
		rpcCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
		created, err := user.CreateUser(rpcCtx, &userpb.CreateUserRequest{Username: account.name, Password: password})
		cancel()
		if status.Code(err) == codes.AlreadyExists {
			return fmt.Errorf("partial seed detected: recreate the compose volumes (account %s already exists)", account.name)
		}
		if err != nil {
			return fmt.Errorf("create %s: %w", account.name, err)
		}
		account.id = created.GetUser().GetId()
		if account.id <= 0 {
			return fmt.Errorf("create %s: missing user ID", account.name)
		}
		for _, asset := range [...]string{quoteAsset, "BTC", "ETH"} {
			rpcCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
			_, err := user.Deposit(rpcCtx, &userpb.DepositRequest{
				UserId: account.id, Asset: asset, Amount: account.available[asset].String(), RefId: "seed:" + account.name + ":" + asset,
			})
			cancel()
			if err != nil {
				return fmt.Errorf("deposit %s for %s: %w", asset, account.name, err)
			}
		}
	}
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		log.Fatalf("demo seed failed: %v", err)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	dsn, err := sqldriver.ParseDSN(cfg.Database.DSN())
	if err != nil {
		return fmt.Errorf("parse database configuration: %w", err)
	}
	// DATETIME has no zone. Encode the backdated rows as UTC regardless of TZ.
	dsn.Loc = time.UTC
	db, err := gorm.Open(mysql.Open(dsn.FormatDSN()), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	pool, err := db.DB()
	if err != nil {
		return fmt.Errorf("open database pool: %w", err)
	}
	defer pool.Close()

	var existing int64
	if err := db.WithContext(ctx).Raw("SELECT COUNT(*) FROM trades WHERE trade_id LIKE 'seed-%'").Scan(&existing).Error; err != nil {
		return fmt.Errorf("check for existing history: %w", err)
	}
	if existing > 0 {
		log.Print("demo data already present")
		return nil
	}

	userConn, err := grpcx.Dial(cfg.GRPC.UserGRPCAddr)
	if err != nil {
		return fmt.Errorf("dial user-svc: %w", err)
	}
	defer userConn.Close()
	orderConn, err := grpcx.Dial(cfg.GRPC.OrderGRPCAddr)
	if err != nil {
		return fmt.Errorf("dial order-svc: %w", err)
	}
	defer orderConn.Close()
	user := userpb.NewUserServiceClient(userConn)
	orders := orderpb.NewOrderServiceClient(orderConn)
	users := newAccounts()
	if err := users.createAndFund(ctx, user); err != nil {
		return err
	}

	rng := rand.New(rand.NewPCG(20261124, 7))
	last, err := seedHistory(ctx, db, user, &users, rng, time.Now())
	if err != nil {
		return err
	}
	book := bookSeeder{orders: orders, users: user, accounts: &users, db: db, rng: rng}
	if err := book.seed(ctx, last); err != nil {
		return err
	}

	var counts [marketCount]int64
	for i, market := range seedMarkets {
		if err := db.WithContext(ctx).Model(&model.Trade{}).Where("symbol = ?", market.symbol).Count(&counts[i]).Error; err != nil {
			return fmt.Errorf("count trades for %s: %w", market.symbol, err)
		}
	}
	log.Printf("demo seed complete: BTC/USDT=%d ETH/USDT=%d", counts[btcMarket], counts[ethMarket])
	return nil
}
