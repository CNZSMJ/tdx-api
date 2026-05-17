package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/injoyai/tdx/internal/appenv"
	"github.com/injoyai/tdx/market/billboard"
)

func main() {
	var (
		dbPath    string
		dataDir   string
		startDate string
		endDate   string
	)
	flag.StringVar(&dbPath, "db", "", "market billboard sqlite path")
	flag.StringVar(&dataDir, "data-dir", appenv.ResolveTDXDataDir("./data/database"), "tdx data directory")
	flag.StringVar(&startDate, "start-date", "", "start date in YYYYMMDD")
	flag.StringVar(&endDate, "end-date", "", "end date in YYYYMMDD")
	flag.Parse()

	if dbPath == "" {
		dbPath = billboard.DefaultDBPath(dataDir)
	}
	store, err := billboard.OpenStore(dbPath)
	if err != nil {
		fatal(err)
	}
	defer store.Close()

	if strings.TrimSpace(endDate) == "" {
		endDate = startDate
	}
	freshness, err := store.Coverage(
		billboard.ParseEastmoneyDate(startDate),
		billboard.ParseEastmoneyDate(endDate),
		billboard.CoreReports(),
	)
	if err != nil {
		fatal(err)
	}
	entries, seats, institutions, stats, err := store.Counts()
	if err != nil {
		fatal(err)
	}
	rawRows, err := store.CountRawRows()
	if err != nil {
		fatal(err)
	}
	printJSON(map[string]any{
		"db":                 dbPath,
		"freshness":          freshness,
		"raw_rows":           rawRows,
		"entries":            entries,
		"seat_trades":        seats,
		"institution_trades": institutions,
		"instrument_stats":   stats,
	})
}

func printJSON(value any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(value)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
