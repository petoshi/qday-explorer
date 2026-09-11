package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"go.sia.tech/core/consensus"
	"go.sia.tech/core/types"
	"go.sia.tech/walletd/v2/wallet"
)

type apiError struct {
	status int
	err    error
}

func (e apiError) Error() string { return e.err.Error() }

type amount struct {
	QDAY   string `json:"qday"`
	Atomic string `json:"atomic"`
}

type blockSummary struct {
	Height       uint64 `json:"height"`
	ID           string `json:"id"`
	Timestamp    string `json:"timestamp"`
	Transactions int    `json:"transactions"`
	Reward       amount `json:"reward"`
	Miner        string `json:"miner"`
}

type txSummary struct {
	ID            string  `json:"id"`
	Kind          string  `json:"kind"`
	Height        *uint64 `json:"height,omitempty"`
	Timestamp     string  `json:"timestamp,omitempty"`
	Confirmations uint64  `json:"confirmations"`
	From          string  `json:"from,omitempty"`
	To            string  `json:"to,omitempty"`
	Value         amount  `json:"value"`
	Fee           amount  `json:"fee"`
	Mempool       bool    `json:"mempool"`
}

func (a *app) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		tip, err := a.wm.Tip()
		ready := err == nil && a.networkSynced(tip)
		if !ready {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ready": false})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ready": true, "height": tip.Height})
	})
	mux.HandleFunc("/api/status", a.api(a.status))
	mux.HandleFunc("/api/blocks", a.api(a.blocks))
	mux.HandleFunc("/api/blocks/", a.api(a.block))
	mux.HandleFunc("/api/transactions/recent", a.api(a.recentTransactions))
	mux.HandleFunc("/api/transactions/", a.api(a.transaction))
	mux.HandleFunc("/api/addresses/", a.api(a.address))
	mux.HandleFunc("/api/search", a.api(a.search))
	mux.HandleFunc("/", a.serveWeb)
	return mux
}

func (a *app) networkSynced(indexed types.ChainIndex) bool {
	if indexed != a.cm.Tip() || indexed.Height == 0 {
		return false
	}
	for _, peer := range a.sy.Peers() {
		if peer.Synced() {
			return true
		}
	}
	return false
}

func (a *app) api(fn func(*http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET required"})
			return
		}
		select {
		case a.queries <- struct{}{}:
			defer func() { <-a.queries }()
		case <-r.Context().Done():
			return
		}
		defer func() {
			if recovered := recover(); recovered != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "explorer query failed"})
			}
		}()
		result, err := fn(r)
		if err != nil {
			status := http.StatusInternalServerError
			var ae apiError
			if errors.As(err, &ae) {
				status, err = ae.status, ae.err
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, result)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (a *app) serveWeb(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	if path.Ext(r.URL.Path) == "" {
		r2 := r.Clone(r.Context())
		r2.URL = new(url.URL)
		*r2.URL = *r.URL
		r2.URL.Path = "/"
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
		a.static.ServeHTTP(w, r2)
		return
	}
	if ext := path.Ext(r.URL.Path); ext == ".js" || ext == ".css" {
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
		a.static.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	a.static.ServeHTTP(w, r)
}

func queryLimit(r *http.Request, fallback, maximum int) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n < 1 {
		return fallback
	}
	if n > maximum {
		return maximum
	}
	return n
}

func queryOffset(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if err != nil || n < 0 {
		return 0
	}
	return min(n, 1_000_000)
}

func qdayAddress(addr types.Address) string {
	return types.QdayAddress(addr).String()
}

func formatAmount(v, unit types.Currency) string {
	q, remainder := new(big.Int), new(big.Int)
	q.QuoRem(v.Big(), unit.Big(), remainder)
	if remainder.Sign() == 0 {
		return q.String()
	}
	decimals := len(unit.ExactString()) - 1
	frac := strings.Repeat("0", decimals-len(remainder.String())) + remainder.String()
	return q.String() + "." + strings.TrimRight(frac, "0")
}

func asAmount(v, unit types.Currency) amount {
	return amount{QDAY: formatAmount(v, unit), Atomic: v.ExactString()}
}

func prettyHashrate(difficulty consensus.Work, seconds int64) string {
	v, ok := new(big.Int).SetString(difficulty.String(), 10)
	if !ok || seconds < 1 {
		return "0 H/s"
	}
	v.Div(v, big.NewInt(seconds))
	units := []string{"H/s", "kH/s", "MH/s", "GH/s", "TH/s", "PH/s", "EH/s", "ZH/s", "YH/s"}
	divisor := big.NewInt(1)
	unit := 0
	for unit+1 < len(units) {
		next := new(big.Int).Mul(divisor, big.NewInt(1000))
		if v.Cmp(next) < 0 {
			break
		}
		divisor, unit = next, unit+1
	}
	if unit == 0 {
		return v.String() + " " + units[unit]
	}
	scaled := new(big.Int).Mul(v, big.NewInt(100))
	scaled.Div(scaled, divisor)
	whole, frac := new(big.Int), new(big.Int)
	whole.QuoRem(scaled, big.NewInt(100), frac)
	return fmt.Sprintf("%s.%02d %s", whole.String(), frac.Int64(), units[unit])
}

func qdayStage(cs consensus.State) (stage string, remaining uint64) {
	if cs.QdayActive(cs.Index.Height) {
		return "ACTIVE", 0
	} else if cs.QdayHeight != 0 {
		if cs.QdayHeight > cs.Index.Height {
			return "COUNTDOWN", cs.QdayHeight - cs.Index.Height
		}
		return "ACTIVE", 0
	}
	return "WAITING", 0
}

func (a *app) status(_ *http.Request) (any, error) {
	tip := a.cm.Tip()
	cs := a.cm.TipState()
	indexed, err := a.wm.Tip()
	if err != nil {
		return nil, err
	}
	stage, remaining := qdayStage(cs)
	unit := cs.QdayUnits(cs.Index.Height)
	minedHeight := min(tip.Height, a.manifest.Network.Qday.MiningBlocks)
	gross := a.manifest.Network.Qday.PremineAmount.Add(a.manifest.Network.Qday.Reward.Mul64(minedHeight))
	var lastBlock string
	if block, ok := a.cm.Block(tip.ID); ok {
		lastBlock = block.Timestamp.UTC().Format(time.RFC3339)
	}
	proofPending := ""
	for _, txn := range a.cm.V2PoolTransactions() {
		if env, err := consensus.ParseQdayEnvelope(txn.ArbitraryData); err == nil && env.Kind == consensus.QdayCanaryProof {
			proofPending = txn.ID().String()
			break
		}
	}
	proofTransaction := ""
	proofBlock := uint64(0)
	if cs.QdayHeight != 0 && cs.QdayHeight >= cs.Network.Qday.ActivationDelay {
		proofBlock = cs.QdayHeight - cs.Network.Qday.ActivationDelay
		if index, ok := a.cm.BestIndex(proofBlock); ok {
			if block, ok := a.cm.Block(index.ID); ok {
				for _, txn := range block.V2Transactions() {
					if kind, _ := transactionKind(txn); kind == "QDAY PROOF" {
						proofTransaction = txn.ID().String()
						break
					}
				}
			}
		}
	}
	return map[string]any{
		"network":             a.manifest.Network.Name,
		"version":             version,
		"genesis":             a.manifest.Genesis.ID().String(),
		"height":              tip.Height,
		"indexedHeight":       indexed.Height,
		"synced":              a.networkSynced(indexed),
		"peers":               len(a.sy.Peers()),
		"mempoolTransactions": len(a.cm.V2PoolTransactions()),
		"lastBlock":           lastBlock,
		"difficulty":          cs.Difficulty.String(),
		"target":              cs.PoWTarget().String(),
		"estimatedHashrate":   prettyHashrate(cs.Difficulty, int64(a.manifest.Network.BlockInterval/time.Second)),
		"blockReward":         asAmount(cs.BlockReward(), unit),
		"grossSupply":         asAmount(gross, unit),
		"unitAtomic":          unit.ExactString(),
		"qday": map[string]any{
			"stage":                  stage,
			"height":                 cs.QdayHeight,
			"blocksRemaining":        remaining,
			"proofPending":           proofPending,
			"proofTransaction":       proofTransaction,
			"proofBlock":             proofBlock,
			"acceptedProofs":         boolToInt(cs.QdayHeight != 0),
			"canary":                 fmt.Sprintf("%x", cs.Network.Qday.Canary),
			"challenge":              "Edwards25519 discrete logarithm",
			"witness":                "canonical nonzero scalar, 32-byte little-endian",
			"proofFee":               asAmount(cs.Network.Qday.ProofFee, unit),
			"activationDelay":        cs.Network.Qday.ActivationDelay,
			"denominationMultiplier": 1_000_000,
			"shieldBlocks":           cs.Network.Qday.ShieldBlocks,
			"decayBlocks":            cs.Network.Qday.DecayBlocks,
			"defendBits":             cs.Network.Qday.DefendBits,
			"failedAttemptsVisible":  false,
		},
		"genesisInscription": a.manifest.GenesisMessage,
		"started":            a.started.Format(time.RFC3339),
	}, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (a *app) blocks(r *http.Request) (any, error) {
	limit, offset := queryLimit(r, 20, 100), queryOffset(r)
	tip := a.cm.Tip()
	if uint64(offset) > tip.Height {
		return map[string]any{"blocks": []blockSummary{}, "tip": tip.Height}, nil
	}
	start := tip.Height - uint64(offset)
	blocks := make([]blockSummary, 0, limit)
	for h := start; len(blocks) < limit; h-- {
		index, ok := a.cm.BestIndex(h)
		if !ok {
			break
		}
		block, ok := a.cm.Block(index.ID)
		if !ok {
			break
		}
		state, ok := a.cm.State(index.ID)
		if !ok {
			break
		}
		blocks = append(blocks, summarizeBlock(block, state))
		if h == 0 {
			break
		}
	}
	return map[string]any{"blocks": blocks, "tip": tip.Height}, nil
}

func summarizeBlock(block types.Block, state consensus.State) blockSummary {
	var reward types.Currency
	miner := ""
	for i, payout := range block.MinerPayouts {
		reward = reward.Add(payout.Value)
		if i == 0 {
			miner = qdayAddress(payout.Address)
		}
	}
	userTransactions := len(block.Transactions)
	for _, txn := range block.V2Transactions() {
		if kind, _ := transactionKind(txn); kind != "MINER MARKER" {
			userTransactions++
		}
	}
	return blockSummary{
		Height:       state.Index.Height,
		ID:           block.ID().String(),
		Timestamp:    block.Timestamp.UTC().Format(time.RFC3339),
		Transactions: userTransactions,
		Reward:       asAmount(reward, state.QdayUnits(state.Index.Height)),
		Miner:        miner,
	}
}

func (a *app) block(r *http.Request) (any, error) {
	value := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/blocks/"), "/")
	if value == "" || len(value) > 80 {
		return nil, apiError{http.StatusBadRequest, errors.New("block height or ID required")}
	}
	var index types.ChainIndex
	if h, err := strconv.ParseUint(value, 10, 64); err == nil {
		var ok bool
		index, ok = a.cm.BestIndex(h)
		if !ok {
			return nil, apiError{http.StatusNotFound, errors.New("block not found")}
		}
	} else {
		var id types.BlockID
		if err := id.UnmarshalText([]byte(strings.ToLower(value))); err != nil {
			return nil, apiError{http.StatusBadRequest, errors.New("invalid block ID")}
		}
		state, ok := a.cm.State(id)
		if !ok {
			return nil, apiError{http.StatusNotFound, errors.New("block not found")}
		}
		index = state.Index
	}
	block, ok := a.cm.Block(index.ID)
	if !ok {
		return nil, apiError{http.StatusNotFound, errors.New("block not found")}
	}
	state, ok := a.cm.State(index.ID)
	if !ok {
		return nil, apiError{http.StatusNotFound, errors.New("block state not found")}
	}
	powState := state
	if state.Index.Height > 0 {
		if parent, found := a.cm.State(block.ParentID); found {
			powState = parent
		}
	}
	unit := state.QdayUnits(state.Index.Height)
	var reward, fees types.Currency
	miner := ""
	for i, payout := range block.MinerPayouts {
		reward = reward.Add(payout.Value)
		if i == 0 {
			miner = qdayAddress(payout.Address)
		}
	}
	txns := make([]txSummary, 0, len(block.V2Transactions()))
	for _, txn := range block.Transactions {
		var value types.Currency
		to := ""
		for i, output := range txn.SiacoinOutputs {
			value = value.Add(output.Value)
			if i == 0 {
				to = qdayAddress(output.Address)
			}
		}
		height := state.Index.Height
		txns = append(txns, txSummary{
			ID:            txn.ID().String(),
			Kind:          "GENESIS PREMINE",
			Height:        &height,
			Timestamp:     block.Timestamp.UTC().Format(time.RFC3339),
			Confirmations: a.cm.Tip().Height - state.Index.Height + 1,
			To:            to,
			Value:         asAmount(value, unit),
			Fee:           asAmount(types.ZeroCurrency, unit),
		})
	}
	markerCount := 0
	for _, txn := range block.V2Transactions() {
		fees = fees.Add(txn.MinerFee)
		summary := summarizeTransaction(txn, &state.Index.Height, block.Timestamp, a.cm.Tip().Height-state.Index.Height+1, unit, false)
		if summary.Kind == "MINER MARKER" {
			markerCount++
			continue
		}
		txns = append(txns, summary)
	}
	stage, remaining := qdayStage(state)
	return map[string]any{
		"height":           state.Index.Height,
		"id":               block.ID().String(),
		"parentID":         block.ParentID.String(),
		"timestamp":        block.Timestamp.UTC().Format(time.RFC3339),
		"nonce":            strconv.FormatUint(block.Nonce, 10),
		"commitment":       block.Header().Commitment.String(),
		"difficulty":       powState.Difficulty.String(),
		"target":           powState.PoWTarget().String(),
		"confirmations":    a.cm.Tip().Height - state.Index.Height + 1,
		"miner":            miner,
		"reward":           asAmount(reward, unit),
		"baseReward":       asAmount(reward.Sub(fees), unit),
		"fees":             asAmount(fees, unit),
		"transactions":     txns,
		"minerMarkerCount": markerCount,
		"qday": map[string]any{
			"stage":           stage,
			"height":          state.QdayHeight,
			"blocksRemaining": remaining,
		},
	}, nil
}

func transactionKind(txn types.V2Transaction) (string, consensus.QdayEnvelope) {
	env, err := consensus.ParseQdayEnvelope(txn.ArbitraryData)
	if err != nil {
		return "UNKNOWN", env
	}
	switch env.Kind {
	case consensus.QdayTransfer:
		return "TRANSFER", env
	case consensus.QdayCoinbase:
		return "MINER MARKER", env
	case consensus.QdayCanaryProof:
		return "QDAY PROOF", env
	default:
		return "UNKNOWN", env
	}
}

func summarizeTransaction(txn types.V2Transaction, height *uint64, timestamp time.Time, confirmations uint64, unit types.Currency, mempool bool) txSummary {
	kind, _ := transactionKind(txn)
	inputAddresses := make(map[types.Address]bool)
	from := ""
	for _, input := range txn.SiacoinInputs {
		inputAddresses[input.Parent.SiacoinOutput.Address] = true
		if from == "" {
			from = qdayAddress(input.Parent.SiacoinOutput.Address)
		}
	}
	var transferred types.Currency
	to := ""
	for _, output := range txn.SiacoinOutputs {
		if !inputAddresses[output.Address] {
			transferred = transferred.Add(output.Value)
			if to == "" {
				to = qdayAddress(output.Address)
			}
		}
	}
	if transferred.IsZero() && len(txn.SiacoinOutputs) > 0 && kind != "QDAY PROOF" {
		for _, output := range txn.SiacoinOutputs {
			transferred = transferred.Add(output.Value)
		}
		to = qdayAddress(txn.SiacoinOutputs[0].Address)
	}
	return txSummary{
		ID:            txn.ID().String(),
		Kind:          kind,
		Height:        height,
		Timestamp:     formatTime(timestamp),
		Confirmations: confirmations,
		From:          from,
		To:            to,
		Value:         asAmount(transferred, unit),
		Fee:           asAmount(txn.MinerFee, unit),
		Mempool:       mempool,
	}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func (a *app) recentTransactions(r *http.Request) (any, error) {
	limit, offset := queryLimit(r, 20, 50), queryOffset(r)
	cs := a.cm.TipState()
	unit := cs.QdayUnits(cs.Index.Height)
	result := make([]txSummary, 0, limit+1)
	skipped := 0
	appendResult := func(summary txSummary) bool {
		if skipped < offset {
			skipped++
			return false
		}
		result = append(result, summary)
		return len(result) > limit
	}
	mempool := 0
	for _, txn := range a.cm.V2PoolTransactions() {
		kind, _ := transactionKind(txn)
		if kind == "MINER MARKER" {
			continue
		}
		mempool++
		if len(result) <= limit {
			appendResult(summarizeTransaction(txn, nil, time.Time{}, 0, unit, true))
		}
	}
	tip := a.cm.Tip()
	for h := tip.Height; len(result) <= limit; h-- {
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		default:
		}
		index, ok := a.cm.BestIndex(h)
		if !ok {
			break
		}
		block, ok := a.cm.Block(index.ID)
		if !ok {
			break
		}
		state, ok := a.cm.State(index.ID)
		if !ok {
			break
		}
		unit := state.QdayUnits(h)
		for i := len(block.V2Transactions()) - 1; i >= 0 && len(result) <= limit; i-- {
			txn := block.V2Transactions()[i]
			kind, _ := transactionKind(txn)
			if kind != "MINER MARKER" {
				height := h
				appendResult(summarizeTransaction(txn, &height, block.Timestamp, tip.Height-h+1, unit, false))
			}
		}
		for i := len(block.Transactions) - 1; i >= 0 && len(result) <= limit; i-- {
			txn := block.Transactions[i]
			var value types.Currency
			to := ""
			for j, output := range txn.SiacoinOutputs {
				value = value.Add(output.Value)
				if j == 0 {
					to = qdayAddress(output.Address)
				}
			}
			height := h
			appendResult(txSummary{
				ID:            txn.ID().String(),
				Kind:          "GENESIS PREMINE",
				Height:        &height,
				Timestamp:     block.Timestamp.UTC().Format(time.RFC3339),
				Confirmations: tip.Height - h + 1,
				To:            to,
				Value:         asAmount(value, unit),
				Fee:           asAmount(types.ZeroCurrency, unit),
			})
		}
		if h == 0 {
			break
		}
	}
	hasMore := len(result) > limit
	if hasMore {
		result = result[:limit]
	}
	return map[string]any{
		"transactions": result,
		"offset":       offset,
		"limit":        limit,
		"hasMore":      hasMore,
		"mempool":      mempool,
	}, nil
}

func (a *app) findTransaction(id types.TransactionID) (types.V2Transaction, *types.ChainIndex, time.Time, bool) {
	if txn, ok := a.cm.V2PoolTransaction(id); ok {
		return txn, nil, time.Time{}, true
	}
	events, err := a.wm.Events([]types.Hash256{types.Hash256(id)})
	if err == nil {
		for _, event := range events {
			if txn, ok := event.Data.(wallet.EventV2Transaction); ok {
				v := types.V2Transaction(txn)
				index := event.Index
				return v, &index, event.Timestamp, true
			}
		}
	}
	// The chain manager can outrun the SQLite index by a block. Bridge that gap.
	tip := a.cm.Tip()
	for h, scanned := tip.Height, 0; scanned < 256; scanned++ {
		index, ok := a.cm.BestIndex(h)
		if !ok {
			break
		}
		block, ok := a.cm.Block(index.ID)
		if ok {
			for _, txn := range block.V2Transactions() {
				if txn.ID() == id {
					copyIndex := index
					return txn, &copyIndex, block.Timestamp, true
				}
			}
		}
		if h == 0 {
			break
		}
		h--
	}
	return types.V2Transaction{}, nil, time.Time{}, false
}

func (a *app) transaction(r *http.Request) (any, error) {
	value := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/transactions/"), "/")
	var id types.TransactionID
	if err := id.UnmarshalText([]byte(strings.ToLower(value))); err != nil {
		return nil, apiError{http.StatusBadRequest, errors.New("invalid transaction ID")}
	}
	for _, txn := range a.manifest.Genesis.Transactions {
		if txn.ID() != id {
			continue
		}
		unit := types.HastingsPerSiacoin
		outputs := make([]map[string]any, 0, len(txn.SiacoinOutputs))
		var total types.Currency
		for i, output := range txn.SiacoinOutputs {
			total = total.Add(output.Value)
			outputs = append(outputs, map[string]any{
				"outputID": txn.SiacoinOutputID(i).String(),
				"address":  qdayAddress(output.Address),
				"value":    asAmount(output.Value, unit),
			})
		}
		return map[string]any{
			"id":            id.String(),
			"kind":          "GENESIS PREMINE",
			"mempool":       false,
			"height":        uint64(0),
			"blockID":       a.manifest.Genesis.ID().String(),
			"timestamp":     a.manifest.Genesis.Timestamp.UTC().Format(time.RFC3339),
			"confirmations": a.cm.Tip().Height + 1,
			"inputs":        []any{},
			"outputs":       outputs,
			"inputTotal":    asAmount(types.ZeroCurrency, unit),
			"outputTotal":   asAmount(total, unit),
			"fee":           asAmount(types.ZeroCurrency, unit),
			"defendNonce":   "0",
			"qdayProof":     false,
		}, nil
	}
	txn, index, timestamp, ok := a.findTransaction(id)
	if !ok {
		return nil, apiError{http.StatusNotFound, errors.New("transaction not found")}
	}
	cs := a.cm.TipState()
	state := cs
	confirmations := uint64(0)
	if index != nil {
		if found, ok := a.cm.State(index.ID); ok {
			state = found
		}
		confirmations = a.cm.Tip().Height - index.Height + 1
	}
	unit := state.QdayUnits(state.Index.Height)
	kind, env := transactionKind(txn)
	inputs := make([]map[string]any, 0, len(txn.SiacoinInputs))
	var inputTotal types.Currency
	for _, input := range txn.SiacoinInputs {
		value := input.Parent.SiacoinOutput.Value
		if index != nil {
			parentState := state
			if block, found := a.cm.Block(index.ID); found {
				if ps, found := a.cm.State(block.ParentID); found {
					parentState = ps
				}
			}
			value = parentState.QdayValue(input.Parent, index.Height)
		}
		inputTotal = inputTotal.Add(value)
		inputs = append(inputs, map[string]any{
			"outputID": input.Parent.ID.String(),
			"address":  qdayAddress(input.Parent.SiacoinOutput.Address),
			"value":    asAmount(value, unit),
		})
	}
	outputs := make([]map[string]any, 0, len(txn.SiacoinOutputs))
	var outputTotal types.Currency
	for i, output := range txn.SiacoinOutputs {
		outputTotal = outputTotal.Add(output.Value)
		outputs = append(outputs, map[string]any{
			"outputID": txn.SiacoinOutputID(txn.ID(), i).String(),
			"address":  qdayAddress(output.Address),
			"value":    asAmount(output.Value, unit),
		})
	}
	var height any
	var blockID string
	if index != nil {
		height, blockID = index.Height, index.ID.String()
	}
	return map[string]any{
		"id":            id.String(),
		"kind":          kind,
		"mempool":       index == nil,
		"height":        height,
		"blockID":       blockID,
		"timestamp":     formatTime(timestamp),
		"confirmations": confirmations,
		"inputs":        inputs,
		"outputs":       outputs,
		"inputTotal":    asAmount(inputTotal, unit),
		"outputTotal":   asAmount(outputTotal, unit),
		"fee":           asAmount(txn.MinerFee, unit),
		"defendNonce":   strconv.FormatUint(env.Nonce, 10),
		"qdayProof":     kind == "QDAY PROOF",
	}, nil
}

func (a *app) address(r *http.Request) (any, error) {
	value, err := url.PathUnescape(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/addresses/"), "/"))
	if err != nil {
		return nil, apiError{http.StatusBadRequest, errors.New("invalid address")}
	}
	qaddr, err := types.ParseQdayAddress(strings.ToLower(value))
	if err != nil {
		return nil, apiError{http.StatusBadRequest, errors.New("invalid QDAY address")}
	}
	addr := types.Address(qaddr)
	balance, err := a.wm.AddressBalance(addr)
	if err != nil {
		return nil, err
	}
	var outputs []wallet.UnspentSiacoinElement
	var basis types.ChainIndex
	for offset := 0; offset < 100_000; offset += 500 {
		batch, b, err := a.wm.AddressSiacoinOutputs(addr, false, offset, 500)
		if err != nil {
			return nil, err
		}
		basis = b
		outputs = append(outputs, batch...)
		if len(batch) < 500 {
			break
		}
	}
	state := a.cm.TipState()
	if indexedState, ok := a.cm.State(basis.ID); ok {
		state = indexedState
	}
	var spendable types.Currency
	shielded, decaying, dead := 0, 0, 0
	for _, output := range outputs {
		value := state.QdayValue(output.SiacoinElement, state.Index.Height)
		spendable = spendable.Add(value)
		if !state.QdayActive(state.Index.Height) {
			continue
		}
		start := max(state.QdayHeight, output.MaturityHeight)
		if state.Index.Height <= start+state.Network.Qday.ShieldBlocks {
			shielded++
		} else if value.IsZero() {
			dead++
		} else {
			decaying++
		}
	}
	limit, offset := queryLimit(r, 25, 100), queryOffset(r)
	events, err := a.wm.AddressEvents(addr, offset, limit+1)
	if err != nil {
		return nil, err
	}
	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}
	unit := state.QdayUnits(state.Index.Height)
	history := make([]map[string]any, 0, len(events))
	for _, event := range events {
		inflow, outflow := event.SiacoinInflow(), event.SiacoinOutflow()
		direction, value := "IN", inflow
		if outflow.Cmp(inflow) > 0 {
			direction, value = "OUT", outflow.Sub(inflow)
		} else if inflow.Cmp(outflow) >= 0 {
			value = inflow.Sub(outflow)
		}
		kind, target, linkID := strings.ToUpper(event.Type), "transaction", event.ID.String()
		if event.Type == wallet.EventTypeMinerPayout {
			kind, target, linkID = "MINER REWARD", "block", event.Index.ID.String()
		} else if event.Type == wallet.EventTypeV1Transaction {
			kind = "GENESIS PREMINE"
		} else if txn, ok := event.Data.(wallet.EventV2Transaction); ok {
			kind, _ = transactionKind(types.V2Transaction(txn))
		}
		history = append(history, map[string]any{
			"id":            event.ID.String(),
			"linkID":        linkID,
			"kind":          kind,
			"target":        target,
			"height":        event.Index.Height,
			"timestamp":     event.Timestamp.UTC().Format(time.RFC3339),
			"confirmations": event.Confirmations,
			"direction":     direction,
			"value":         asAmount(value, unit),
		})
	}
	return map[string]any{
		"address":        qaddr.String(),
		"indexedHeight":  basis.Height,
		"balance":        asAmount(spendable, unit),
		"nominalBalance": asAmount(balance.Siacoins, unit),
		"immature":       asAmount(balance.ImmatureSiacoins, unit),
		"liveOutputs":    len(outputs),
		"shielded":       shielded,
		"decaying":       decaying,
		"expired":        dead,
		"history":        history,
		"offset":         offset,
		"limit":          limit,
		"hasMore":        hasMore,
	}, nil
}

func (a *app) search(r *http.Request) (any, error) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" || len(q) > 160 {
		return nil, apiError{http.StatusBadRequest, errors.New("search value required")}
	}
	if h, err := strconv.ParseUint(q, 10, 64); err == nil {
		if _, ok := a.cm.BestIndex(h); ok {
			return map[string]string{"type": "block", "path": "/block/" + strconv.FormatUint(h, 10)}, nil
		}
		return nil, apiError{http.StatusNotFound, errors.New("block not found")}
	}
	if addr, err := types.ParseQdayAddress(strings.ToLower(q)); err == nil {
		return map[string]string{"type": "address", "path": "/address/" + addr.String()}, nil
	}
	if len(q) == 64 {
		var blockID types.BlockID
		if blockID.UnmarshalText([]byte(strings.ToLower(q))) == nil {
			if _, ok := a.cm.State(blockID); ok {
				return map[string]string{"type": "block", "path": "/block/" + blockID.String()}, nil
			}
		}
		var txid types.TransactionID
		if txid.UnmarshalText([]byte(strings.ToLower(q))) == nil {
			for _, txn := range a.manifest.Genesis.Transactions {
				if txn.ID() == txid {
					return map[string]string{"type": "transaction", "path": "/transaction/" + txid.String()}, nil
				}
			}
			if _, _, _, ok := a.findTransaction(txid); ok {
				return map[string]string{"type": "transaction", "path": "/transaction/" + txid.String()}, nil
			}
		}
	}
	return nil, apiError{http.StatusNotFound, errors.New("nothing found")}
}
