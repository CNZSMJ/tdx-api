package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/injoyai/tdx/eastmoney/rpt"
	"github.com/injoyai/tdx/internal/appenv"
	"github.com/injoyai/tdx/market/billboard"
)

func main() {
	var (
		dbPath      string
		dataDir     string
		startDate   string
		endDate     string
		dryRun      bool
		pageSize    int
		concurrency int
	)
	flag.StringVar(&dbPath, "db", "", "market billboard sqlite path")
	flag.StringVar(&dataDir, "data-dir", appenv.ResolveTDXDataDir("./data/database"), "tdx data directory")
	flag.StringVar(&startDate, "start-date", "20250101", "start date in YYYYMMDD")
	flag.StringVar(&endDate, "end-date", time.Now().Format("20060102"), "end date in YYYYMMDD")
	flag.BoolVar(&dryRun, "dry-run", false, "print sync plan without writing")
	flag.IntVar(&pageSize, "page-size", 5000, "Eastmoney RPT page size")
	flag.IntVar(&concurrency, "concurrency", 3, "seat detail concurrency placeholder")
	flag.Parse()

	dates, err := datesInRange(startDate, endDate)
	if err != nil {
		fatal(err)
	}
	if dbPath == "" {
		dbPath = billboard.DefaultDBPath(dataDir)
	}
	if dryRun {
		printJSON(map[string]any{
			"db":          dbPath,
			"start_date":  startDate,
			"end_date":    endDate,
			"dates":       dates,
			"reports":     billboard.AllReports(),
			"page_size":   pageSize,
			"concurrency": concurrency,
		})
		return
	}

	store, err := billboard.OpenStore(dbPath)
	if err != nil {
		fatal(err)
	}
	defer store.Close()

	syncer := billboard.NewSyncer(billboard.SyncerConfig{
		Store:           store,
		Client:          rpt.NewClient(rpt.ClientConfig{PageSize: pageSize, RetryCount: 3, Timeout: 10 * time.Second}),
		PageSize:        pageSize,
		SeatConcurrency: concurrency,
	})
	result, err := syncer.SyncDates(context.Background(), dates)
	if err != nil {
		fatal(err)
	}
	printJSON(result)
}

func datesInRange(startDate, endDate string) ([]string, error) {
	startDate = billboard.ParseEastmoneyDate(strings.TrimSpace(startDate))
	endDate = billboard.ParseEastmoneyDate(strings.TrimSpace(endDate))
	start, err := time.ParseInLocation("20060102", startDate, time.Local)
	if err != nil {
		return nil, fmt.Errorf("invalid start-date %q", startDate)
	}
	end, err := time.ParseInLocation("20060102", endDate, time.Local)
	if err != nil {
		return nil, fmt.Errorf("invalid end-date %q", endDate)
	}
	if end.Before(start) {
		return nil, fmt.Errorf("end-date %s is before start-date %s", endDate, startDate)
	}
	dates := make([]string, 0, int(end.Sub(start).Hours()/24)+1)
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		dates = append(dates, d.Format("20060102"))
	}
	return dates, nil
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
