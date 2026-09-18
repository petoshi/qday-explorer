package main

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.sia.tech/core/consensus"
	"go.sia.tech/core/types"
	"go.sia.tech/coreutils/chain"
	"go.sia.tech/walletd/v2/wallet"
)

func TestConnectionCountFromNode(t *testing.T) {
	manifest := chain.QdayDevnet()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/network-status" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"network":     manifest.Network.Name,
			"connections": 42,
		})
	}))
	a := app{
		manifest:      manifest,
		nodeStatusURL: server.URL + "/api/network-status",
		nodeHTTP:      server.Client(),
	}
	if got := a.connectionCount(context.Background()); got != 42 {
		t.Fatalf("got %d connections, want 42", got)
	}
	server.Close()
	if got := a.connectionCount(context.Background()); got != 42 {
		t.Fatalf("lost cached connection count: got %d, want 42", got)
	}
}

func TestNodeSupply(t *testing.T) {
	manifest := chain.QdayDevnet()
	var invalid atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/supply" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		current := amount{QDAY: "458", Atomic: types.Siacoins(458).ExactString()}
		if invalid.Load() {
			current = amount{QDAY: "459", Atomic: types.Siacoins(459).ExactString()}
		}
		_ = json.NewEncoder(w).Encode(supplyStatus{
			Network:             manifest.Network.Name,
			Height:              10,
			Synced:              true,
			UnitAtomic:          types.HastingsPerSiacoin.ExactString(),
			IssuedSupply:        amount{QDAY: "508", Atomic: types.Siacoins(508).ExactString()},
			BurnedSupply:        amount{QDAY: "50", Atomic: types.Siacoins(50).ExactString()},
			CurrentSupply:       current,
			ImmatureSupply:      amount{QDAY: "8", Atomic: types.Siacoins(8).ExactString()},
			CirculatingSupply:   amount{QDAY: "450", Atomic: types.Siacoins(450).ExactString()},
			MaximumIssuedSupply: amount{QDAY: "8500000", Atomic: types.Siacoins(8_500_000).ExactString()},
			UnspentOutputs:      3,
		})
	}))
	defer server.Close()
	a := app{manifest: manifest, nodeStatusURL: server.URL + "/api/network-status", nodeHTTP: server.Client()}
	supply, err := a.nodeSupply(context.Background())
	if err != nil {
		t.Fatal(err)
	} else if supply.CirculatingSupply.QDAY != "450" || supply.Height != 10 {
		t.Fatalf("wrong supply: %+v", supply)
	}
	invalid.Store(true)
	if _, err := a.nodeSupply(context.Background()); err == nil {
		t.Fatal("accepted inconsistent node supply")
	}
}

func TestFormatAmount(t *testing.T) {
	unit := types.HastingsPerSiacoin
	for _, test := range []struct {
		value types.Currency
		want  string
	}{
		{types.ZeroCurrency, "0"},
		{types.Siacoins(8), "8"},
		{unit.Div64(1000), "0.001"},
		{types.Siacoins(8).Add(unit.Div64(1000)), "8.001"},
	} {
		if got := formatAmount(test.value, unit); got != test.want {
			t.Fatalf("formatAmount(%v): got %q, want %q", test.value, got, test.want)
		}
	}
}

func TestTransactionKind(t *testing.T) {
	for _, test := range []struct {
		kind byte
		want string
	}{
		{consensus.QdayTransfer, "TRANSFER"},
		{consensus.QdayCoinbase, "MINER MARKER"},
		{consensus.QdayMiningWork, "MINING WORK"},
		{consensus.QdayCanaryProof, "QDAY PROOF"},
	} {
		txn := types.V2Transaction{ArbitraryData: (consensus.QdayEnvelope{Kind: test.kind}).Encode()}
		if got, _ := transactionKind(txn); got != test.want {
			t.Fatalf("kind %d: got %q, want %q", test.kind, got, test.want)
		}
	}
	txn := types.V2Transaction{
		ArbitraryData:  (consensus.QdayEnvelope{Kind: consensus.QdayTransfer}).Encode(),
		SiacoinOutputs: []types.SiacoinOutput{{Address: types.VoidAddress, Value: types.Siacoins(10)}},
	}
	if got, _ := transactionKind(txn); got != "BURN" {
		t.Fatalf("void transfer classified as %q", got)
	}
	summary := summarizeTransaction(txn, nil, time.Time{}, 0, types.HastingsPerSiacoin, true)
	if summary.Kind != "BURN" || summary.To != "BURN" || summary.Value.QDAY != "10" {
		t.Fatalf("wrong burn summary: %+v", summary)
	}
	block := types.Block{Transactions: []types.Transaction{{}}, V2: &types.V2BlockData{Transactions: []types.V2Transaction{
		{ArbitraryData: (consensus.QdayEnvelope{Kind: consensus.QdayCoinbase}).Encode()},
		{ArbitraryData: (consensus.QdayEnvelope{Kind: consensus.QdayMiningWork}).Encode()},
		{ArbitraryData: (consensus.QdayEnvelope{Kind: consensus.QdayTransfer}).Encode()},
	}}}
	if got := blockUserTransactionCount(block); got != 2 {
		t.Fatalf("counted %d user transactions, want 2", got)
	}
}

func TestPrettyHashrateValue(t *testing.T) {
	if got := prettyHashrateValue(big.NewInt(1_320_000_000_000), 1); got != "1.32 TH/s" {
		t.Fatalf("got %q", got)
	}
}

func TestExplicitBurnFromAddress(t *testing.T) {
	var premine, stranger types.Address
	premine[0] = 1
	stranger[0] = 2
	burn := func(source types.Address) types.V2Transaction {
		return types.V2Transaction{
			SiacoinInputs: []types.V2SiacoinInput{{
				Parent: types.SiacoinElement{SiacoinOutput: types.SiacoinOutput{
					Address: source,
					Value:   types.Siacoins(100),
				}},
			}},
			SiacoinOutputs: []types.SiacoinOutput{
				{Address: types.VoidAddress, Value: types.Siacoins(25)},
				{Address: premine, Value: types.Siacoins(74)},
			},
		}
	}
	if got := explicitBurnFromAddress(burn(premine), premine); got != types.Siacoins(25) {
		t.Fatalf("got %v burned from premine, want 25 QDAY", got)
	}
	if got := explicitBurnFromAddress(burn(stranger), premine); !got.IsZero() {
		t.Fatalf("attributed another address's burn to premine: %v", got)
	}
}

func TestAddressEventFlowSeparatesValueAndFee(t *testing.T) {
	var source, destination types.Address
	source[0], destination[0] = 1, 2
	value := types.HastingsPerSiacoin.Div64(1000)
	fee := types.Siacoins(240)
	input := types.Siacoins(300)
	txn := types.V2Transaction{
		SiacoinInputs: []types.V2SiacoinInput{{
			Parent: types.SiacoinElement{SiacoinOutput: types.SiacoinOutput{
				Address: source,
				Value:   input,
			}},
		}},
		SiacoinOutputs: []types.SiacoinOutput{
			{Address: destination, Value: value},
			{Address: source, Value: input.Sub(value).Sub(fee)},
		},
		MinerFee: fee,
	}
	event := wallet.Event{
		Type:     wallet.EventTypeV2Transaction,
		Data:     wallet.EventV2Transaction(txn),
		Relevant: []types.Address{source},
	}
	direction, gotValue, gotFee := addressEventFlow(event)
	if direction != "OUT" || gotValue != value || gotFee != fee {
		t.Fatalf("got %s, value %v, fee %v; want OUT, value %v, fee %v", direction, gotValue, gotFee, value, fee)
	}

	event.Relevant = []types.Address{destination}
	direction, gotValue, gotFee = addressEventFlow(event)
	if direction != "IN" || gotValue != value || gotFee != fee {
		t.Fatalf("got %s, value %v, fee %v; want IN, value %v, fee %v", direction, gotValue, gotFee, value, fee)
	}

	burnValue := types.Siacoins(36_450)
	burnFee := types.HastingsPerSiacoin.Div64(1000)
	input = types.Siacoins(40_000)
	txn = types.V2Transaction{
		SiacoinInputs: []types.V2SiacoinInput{{
			Parent: types.SiacoinElement{SiacoinOutput: types.SiacoinOutput{
				Address: source,
				Value:   input,
			}},
		}},
		SiacoinOutputs: []types.SiacoinOutput{
			{Address: types.VoidAddress, Value: burnValue},
			{Address: source, Value: input.Sub(burnValue).Sub(burnFee)},
		},
		MinerFee: burnFee,
	}
	event.Data = wallet.EventV2Transaction(txn)
	event.Relevant = []types.Address{source}
	direction, gotValue, gotFee = addressEventFlow(event)
	if direction != "OUT" || gotValue != burnValue || gotFee != burnFee {
		t.Fatalf("got %s, value %v, fee %v; want OUT, value %v, fee %v", direction, gotValue, gotFee, burnValue, burnFee)
	}
}

func TestQueryRichBalances(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE sia_addresses (id INTEGER PRIMARY KEY, sia_address BLOB NOT NULL);
		CREATE TABLE siacoin_elements (
			address_id INTEGER NOT NULL,
			siacoin_value BLOB NOT NULL,
			maturity_height INTEGER NOT NULL,
			spent_index_id INTEGER
		);`); err != nil {
		t.Fatal(err)
	}
	var first, second, immature, spent types.Address
	first[0], second[0], immature[0], spent[0] = 1, 2, 3, 4
	addresses := []types.Address{first, second, immature, spent, types.VoidAddress}
	for i, address := range addresses {
		if _, err := db.Exec(`INSERT INTO sia_addresses (id, sia_address) VALUES (?, ?)`, i+1, address[:]); err != nil {
			t.Fatal(err)
		}
	}
	encodeCurrency := func(value types.Currency) []byte {
		buf := make([]byte, 16)
		binary.BigEndian.PutUint64(buf, value.Hi)
		binary.BigEndian.PutUint64(buf[8:], value.Lo)
		return buf
	}
	insert := func(addressID int, value uint32, maturity int, spentIndex any) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO siacoin_elements VALUES (?, ?, ?, ?)`, addressID, encodeCurrency(types.Siacoins(value)), maturity, spentIndex); err != nil {
			t.Fatal(err)
		}
	}
	insert(1, 100, 0, nil)
	insert(1, 25, 5, nil)
	insert(2, 200, 0, nil)
	insert(3, 500, 11, nil)
	insert(4, 1000, 0, 9)
	insert(5, 2000, 0, nil)
	index := types.ChainIndex{Height: 10}
	network := chain.QdayDevnet().Network
	state := network.GenesisState()
	state.Index = index
	entries, count, err := queryRichBalances(db, state, index)
	if err != nil {
		t.Fatal(err)
	} else if count != 2 || len(entries) != 2 {
		t.Fatalf("got %d ranked addresses and %d entries, want 2", count, len(entries))
	} else if entries[0].Address != second || entries[0].Value != types.Siacoins(200) {
		t.Fatalf("wrong first entry: %+v", entries[0])
	} else if entries[1].Address != first || entries[1].Value != types.Siacoins(125) {
		t.Fatalf("wrong second entry: %+v", entries[1])
	}
}

func TestPaginationIndexCounts(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE sia_addresses (id INTEGER PRIMARY KEY, sia_address BLOB NOT NULL);
		CREATE TABLE events (id INTEGER PRIMARY KEY, event_type TEXT NOT NULL);
		CREATE TABLE event_addresses (event_id INTEGER NOT NULL, address_id INTEGER NOT NULL);`); err != nil {
		t.Fatal(err)
	}
	var first, second types.Address
	first[0], second[0] = 1, 2
	if _, err := db.Exec(`INSERT INTO sia_addresses VALUES (1, ?), (2, ?)`, first[:], second[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO events VALUES
		(1, ?), (2, ?), (3, ?), (4, ?)`,
		wallet.EventTypeV1Transaction,
		wallet.EventTypeV2Transaction,
		wallet.EventTypeV2Transaction,
		wallet.EventTypeMinerPayout,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO event_addresses VALUES (1, 1), (2, 1), (3, 2), (4, 1)`); err != nil {
		t.Fatal(err)
	}
	index := &richListIndex{db: db}
	chainIndex := types.ChainIndex{Height: 12}
	if got, err := index.transactionEventCount(chainIndex); err != nil {
		t.Fatal(err)
	} else if got != 3 {
		t.Fatalf("got %d transaction events, want 3", got)
	}
	if _, err := db.Exec(`INSERT INTO events VALUES (5, ?)`, wallet.EventTypeV2Transaction); err != nil {
		t.Fatal(err)
	}
	if got, err := index.transactionEventCount(chainIndex); err != nil {
		t.Fatal(err)
	} else if got != 3 {
		t.Fatalf("cache returned %d transaction events, want 3", got)
	}
	chainIndex.Height++
	if got, err := index.transactionEventCount(chainIndex); err != nil {
		t.Fatal(err)
	} else if got != 4 {
		t.Fatalf("refreshed count returned %d transaction events, want 4", got)
	}
	if got, err := index.addressEventCount(first); err != nil {
		t.Fatal(err)
	} else if got != 3 {
		t.Fatalf("got %d address events, want 3", got)
	}
}

func TestPageOffset(t *testing.T) {
	for _, test := range []struct {
		offset int
		limit  int
		total  int
		want   int
	}{
		{0, 50, 0, 0},
		{0, 50, 101, 0},
		{49, 50, 101, 0},
		{50, 50, 101, 50},
		{1_000_000, 50, 101, 100},
	} {
		if got := pageOffset(test.offset, test.limit, test.total); got != test.want {
			t.Fatalf("pageOffset(%d, %d, %d): got %d, want %d", test.offset, test.limit, test.total, got, test.want)
		}
	}
}
