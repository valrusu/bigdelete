package bigdelete

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const (
	defaultThreads = 20
	defaultCommit  = 1237
	defaultGroup   = 1
)

func getTempDir() string {
	if tmpdir := os.Getenv("TMPDIR"); tmpdir != "" {
		return tmpdir
	}
	return "/tmp"
}

type ConfigFile struct {
	Parallel          int           `json:"parallel"`
	DeleteRowidFiles  string        `json:"deleteRowidFiles"`
	CleanupRowidFiles bool          `json:"cleanupRowidFiles"`
	DeleteOnly        bool          `json:"deleteOnly"`
	Tables            []TableConfig `json:"tables"`
}

type TableConfig struct {
	Table   string `json:"table"`
	SQL     string `json:"sql"`
	Threads int    `json:"threads"`
	Commit  int    `json:"commit"`
	Group   int    `json:"group"`
	Enabled bool   `json:"enabled"`
}

func RunConfigFile(configFile string, parallel int, extractOnly bool) Progerr {
	cfg, perr := loadConfigFile(configFile)
	if perr.Err != nil {
		return perr
	}

	if extractOnly && cfg.DeleteOnly {
		return Progerr{fmt.Errorf("cannot use -extractonly with deleteOnly setting"), "cannot use -extractonly with deleteOnly setting", 21}
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

	return runTablePool(db, cfg, parallel, extractOnly)
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

	if strings.TrimSpace(cfg.DeleteRowidFiles) == "" {
		cfg.DeleteRowidFiles = getTempDir()
	}

	if err := os.MkdirAll(cfg.DeleteRowidFiles, 0755); err != nil {
		return cfg, Progerr{err, "cannot create rowid directory " + cfg.DeleteRowidFiles, 22}
	}

	for i, t := range cfg.Tables {
		if strings.TrimSpace(t.Table) == "" {
			return cfg, Progerr{fmt.Errorf("tables[%d]: missing \"table\"", i), fmt.Sprintf("tables[%d]: missing \"table\"", i), 9}
		}
		if !cfg.DeleteOnly && strings.TrimSpace(t.SQL) == "" {
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
		if !t.Enabled {
			t.Enabled = true
			cfg.Tables[i] = t
		}
	}

	return cfg, Progerr{nil, "", 0}
}

func runTablePool(db *sql.DB, cfg ConfigFile, parallel int, extractOnly bool) Progerr {
	work := make(chan TableConfig)
	errs := make(chan Progerr, len(cfg.Tables))
	var wg sync.WaitGroup

	wg.Add(parallel)
	for w := 0; w < parallel; w++ {
		go func() {
			defer wg.Done()
			for t := range work {
				if !t.Enabled {
					continue
				}
				if perr := runOneTable(db, t, cfg.DeleteRowidFiles, cfg.CleanupRowidFiles, cfg.DeleteOnly, extractOnly); perr.Err != nil {
					errs <- perr
				}
			}
		}()
	}

	for _, t := range cfg.Tables {
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
		return Progerr{fmt.Errorf("%d of %d table(s) failed", len(msgs), len(cfg.Tables)), strings.Join(msgs, "; "), code}
	}
	return Progerr{nil, "", 0}
}

func runOneTable(db *sql.DB, t TableConfig, rowidDir string, cleanup bool, configDeleteOnly bool, extractOnly bool) Progerr {
	var tmpPath string
	var perr Progerr
	var shouldCleanup bool

	if configDeleteOnly {
		tmpPath, perr = findRowidFile(rowidDir, t.Table)
		if perr.Err != nil {
			return perr
		}
		shouldCleanup = false
	} else {
		tmpPath, perr = extractRowids(db, t, rowidDir)
		if perr.Err != nil {
			return perr
		}
		if extractOnly {
			if DebugFlag {
				log.Println("table", t.Table, "extracted rowids to", tmpPath)
			}
			return Progerr{nil, "", 0}
		}
		shouldCleanup = cleanup
	}

	defer func() {
		if shouldCleanup && !DebugFlag {
			os.Remove(tmpPath)
		} else if DebugFlag {
			log.Println("table", t.Table, "keeping rowid temp file", tmpPath)
		}
	}()

	f, err := os.Open(tmpPath)
	if err != nil {
		return Progerr{err, "cannot open rowid file for " + t.Table, 17}
	}
	defer f.Close()

	return runDelete(db, t.Table, t.Threads, t.Commit, t.Group, f)
}

var tableNameSanitizer = regexp.MustCompile(`[^A-Za-z0-9_.]+`)

func extractRowids(db *sql.DB, t TableConfig, rowidDir string) (string, Progerr) {
	rows, err := db.Query(t.SQL)
	if err != nil {
		return "", Progerr{err, "rowid extraction query failed for " + t.Table, 13}
	}
	defer rows.Close()

	safeName := tableNameSanitizer.ReplaceAllString(t.Table, "_")
	tmp, err := os.CreateTemp(rowidDir, "bigdelete-"+safeName+"-*.rowids")
	if err != nil {
		return "", Progerr{err, "cannot create rowid temp file for " + t.Table, 17}
	}
	tmpPath := tmp.Name()
	w := bufio.NewWriter(tmp)

	var rid string
	for rows.Next() {
		if err := rows.Scan(&rid); err != nil {
			tmp.Close()
			os.Remove(tmpPath)
			return "", Progerr{err, "rowid scan failed for " + t.Table, 16}
		}
		if _, err := w.WriteString(rid + "\n"); err != nil {
			tmp.Close()
			os.Remove(tmpPath)
			return "", Progerr{err, "cannot write rowid temp file for " + t.Table, 18}
		}
	}

	if err := rows.Err(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return "", Progerr{err, "rowid extraction failed for " + t.Table, 19}
	}

	if err := w.Flush(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return "", Progerr{err, "cannot flush rowid temp file for " + t.Table, 18}
	}

	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return "", Progerr{err, "cannot close rowid temp file for " + t.Table, 18}
	}

	return tmpPath, Progerr{nil, "", 0}
}

func findRowidFile(rowidDir string, tableName string) (string, Progerr) {
	safeName := tableNameSanitizer.ReplaceAllString(tableName, "_")
	pattern := "bigdelete-" + safeName + "-*.rowids"

	entries, err := os.ReadDir(rowidDir)
	if err != nil {
		return "", Progerr{err, "cannot read rowid directory for " + tableName, 23}
	}

	var matches []fs.DirEntry
	for _, entry := range entries {
		if matched, _ := filepath.Match(pattern, entry.Name()); matched {
			matches = append(matches, entry)
		}
	}

	if len(matches) == 0 {
		return "", Progerr{fmt.Errorf("no rowid file found for " + tableName), "no rowid file found for " + tableName + " in " + rowidDir, 24}
	}

	if len(matches) > 1 {
		return "", Progerr{fmt.Errorf("multiple rowid files found for " + tableName), fmt.Sprintf("multiple rowid files found for %s in %s; use -extractonly to regenerate", tableName, rowidDir), 25}
	}

	return filepath.Join(rowidDir, matches[0].Name()), Progerr{nil, "", 0}
}
