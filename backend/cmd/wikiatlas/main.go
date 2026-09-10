// Command wikiatlas is the WikiAltas v2 Go backend.
//
// Usage:
//
//	go run ./cmd/wikiatlas -db ./data/wikiatlas.db -addr :8080
//	go run ./cmd/wikiatlas -db ./data/wikiatlas.db -addr :8080 -seed -skill /root/WikiAltas/skills/wiki-writing
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/httpapi"
	"wikiatlas/backend/internal/run"
	"wikiatlas/backend/internal/store"
)

func main() {
	dbPath := flag.String("db", "./data/wikiatlas.db", "SQLite database path")
	addr := flag.String("addr", ":8080", "HTTP listen address")
	seed := flag.Bool("seed", false, "seed a sample universe if the database is empty")
	skillRoot := flag.String("skill", "", "wiki-writing skill root (default: settings.skillRoot or /root/WikiAltas/skills/wiki-writing)")
	flag.Parse()

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// 配置来源：settings 表（SQLite）。首次启动把 env 里的 WIKIATLAS_* 导入一次，
	// 之后一律以设置页为准（热生效，不需要重启）。
	if imported, err := st.ImportEnvSettings(); err != nil {
		log.Printf("import env settings: %v", err)
	} else if imported {
		log.Printf("settings imported from env (后续请在设置页修改)")
	}

	if *seed {
		if err := seedIfEmpty(st); err != nil {
			log.Printf("seed skipped: %v", err)
		}
	}

	mgr := run.NewManager(st, nil)
	if *skillRoot != "" {
		mgr.SetSkillRoot(*skillRoot)
	}
	mgr.Start()
	defer mgr.Stop()

	srv := httpapi.New(st, mgr)
	server := &http.Server{
		Addr:    *addr,
		Handler: srv.Handler(),
	}

	go func() {
		log.Printf("WikiAltas backend listening on %s (db=%s skill=%s)", *addr, *dbPath, mgr.SkillRoot())
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("shutting down…")
	_ = server.Close()
}

func seedIfEmpty(st *store.Store) error {
	nodes, err := st.ListTree()
	if err != nil {
		return err
	}
	if len(nodes) > 0 {
		return nil
	}
	log.Println("seeding sample universe…")
	universe, err := st.CreateWork(domain.CreateWorkBody{
		Kind:  domain.WorkKindUniverse,
		Title: "猎魔人",
	})
	if err != nil {
		return fmt.Errorf("seed universe: %w", err)
	}
	seriesMedium := domain.MediumGame
	series, err := st.CreateWork(domain.CreateWorkBody{
		ParentID: &universe.ID,
		Kind:     domain.WorkKindSeries,
		Medium:   &seriesMedium,
		Title:    "巫师系列",
	})
	if err != nil {
		return fmt.Errorf("seed series: %w", err)
	}
	game := domain.MediumGame
	if _, err := st.CreateWork(domain.CreateWorkBody{
		ParentID: &series.ID,
		Kind:     domain.WorkKindWork,
		Medium:   &game,
		Title:    "巫师3：狂猎",
	}); err != nil {
		return fmt.Errorf("seed work: %w", err)
	}
	book := domain.MediumBook
	if _, err := st.CreateWork(domain.CreateWorkBody{
		ParentID: &universe.ID,
		Kind:     domain.WorkKindWork,
		Medium:   &book,
		Title:    "白狼崛起",
	}); err != nil {
		return fmt.Errorf("seed work: %w", err)
	}
	log.Println("seed complete")
	return nil
}
