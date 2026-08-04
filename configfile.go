package bigdelete

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
)

const (
	defaultThreads = 20
	defaultCommit  = 1237
	defaultGroup   = 1
)

type ConfigFile struct {
	Parallel int           `json:"parallel"`
	Tables   []TableConfig `json:"tables"`
}

type TableConfig struct {
	Table   string `json:"table"`
	SQL     string `json:"sql"`
	Threads int    `json:"threads"`
	Commit  int    `json:"commit"`
	Group   int    `json:"group"`
}

func RunConfigFile(configFile string, parallel int) Progerr {
	cfg, perr := loadConfigFile(configFile)
	if perr.Err != nil {
		return perr
	}

	if parallel <= 0 {
		parallel = cfg.Parallel
	}
	if parallel < 1 {
		parallel = 1
	}

	db, perr := connectDB()
	if perr.Err != nil {
		return perr
	}
	defer db.Close()

	return runTablePool(db, cfg.Tables, parallel)
}

func loadConfigFile(path string) (ConfigFile, Progerr) {
	var cfg ConfigFile

	f, err := os.Open(path)
	if err != nil {
		return cfg, Progerr{err, "cannot open config file " + path, 4}
	}
	defer f.Close()

	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		return cfg, Progerr{err, "cannot parse config file " + path, 5}
	}

	if cfg.Parallel == 0 {
		cfg.Parallel = 1
	}

	for i, t := range cfg.Tables {
		if strings.TrimSpace(t.Table) == "" {
			return cfg, Progerr{fmt.Errorf("tables[%d]: missing \"table\"", i), fmt.Sprintf("tables[%d]: missing \"table\"", i), 9}
		}
		if strings.TrimSpace(t.SQL) == "" {
			return cfg, Progerr{fmt.Errorf("tables[%d]: missing \"sql\"", i), fmt.Sprintf("tables[%d]: missing \"sql\"", i), 9}
		}
		if t.Threads == 0 {
			t.Threads = defaultThreads
			cfg.Tables[i] = t
		}
		if t.Commit == 0 {
			t.Commit = defaultCommit
			cfg.Tables[i] = t
		}
		if t.Group == 0 {
			t.Group = defaultGroup
			cfg.Tables[i] = t
		}
	}

	return cfg, Progerr{nil, "", 0}
}

func runTablePool(db *sql.DB, tables []TableConfig, parallel int) Progerr {
	work := make(chan TableConfig)
	errs := make(chan Progerr, len(tables))
	var wg sync.WaitGroup

	wg.Add(parallel)
	for w := 0; w < parallel; w++ {
		go func() {
			defer wg.Done()
			for t := range work {
				if perr := runOneTable(db, t); perr.Err != nil {
					errs <- perr
				}
			}
		}()
	}

	for _, t := range tables {
		work <- t
	}
	close(work)
	wg.Wait()
	close(errs)

	var msgs []string
	code := 0
	for perr := range errs {
		log.Printf("table run failed: %s: %v", perr.Msg, perr.Err)
		msgs = append(msgs, fmt.Sprintf("%s: %v", perr.Msg, perr.Err))
		code = perr.Code
	}
	if len(msgs) > 0 {
		return Progerr{fmt.Errorf("%d of %d table(s) failed", len(msgs), len(tables)), strings.Join(msgs, "; "), code}
	}
	return Progerr{nil, "", 0}
}

func runOneTable(db *sql.DB, cfg TableConfig) Progerr {
	tmpPath, perr := extractRowids(db, cfg)
	if perr.Err != nil {
		return perr
	}
	defer func() {
		if DebugFlag {
			log.Println("table", cfg.Table, "keeping rowid temp file", tmpPath)
		} else {
			os.Remove(tmpPath)
		}
	}()

	f, err := os.Open(tmpPath)
	if err != nil {
		return Progerr{err, "cannot reopen rowid temp file for " + cfg.Table, 17}
	}
	defer f.Close()

	return runDelete(db, cfg.Table, cfg.Threads, cfg.Commit, cfg.Group, f)
}

var tableNameSanitizer = regexp.MustCompile(`[^A-Za-z0-9_.]+`)

func extractRowids(db *sql.DB, cfg TableConfig) (string, Progerr) {
	rows, err := db.Query(cfg.SQL)
	if err != nil {
		return "", Progerr{err, "rowid extraction query failed for " + cfg.Table, 13}
	}
	defer rows.Close()

	safeName := tableNameSanitizer.ReplaceAllString(cfg.Table, "_")
	tmp, err := os.CreateTemp("", "bigdelete-"+safeName+"-*.rowids")
	if err != nil {
		return "", Progerr{err, "cannot create rowid temp file for " + cfg.Table, 17}
	}
	tmpPath := tmp.Name()
	w := bufio.NewWriter(tmp)

	var rid string
	for rows.Next() {
		if err := rows.Scan(&rid); err != nil {
			tmp.Close()
			os.Remove(tmpPath)
			return "", Progerr{err, "rowid scan failed for " + cfg.Table, 16}
		}
		if _, err := w.WriteString(rid + "\n"); err != nil {
			tmp.Close()
			os.Remove(tmpPath)
			return "", Progerr{err, "cannot write rowid temp file for " + cfg.Table, 18}
		}
	}

	if err := rows.Err(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return "", Progerr{err, "rowid extraction failed for " + cfg.Table, 19}
	}

	if err := w.Flush(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return "", Progerr{err, "cannot flush rowid temp file for " + cfg.Table, 18}
	}

	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return "", Progerr{err, "cannot close rowid temp file for " + cfg.Table, 18}
	}

	return tmpPath, Progerr{nil, "", 0}
}
