package bigdelete

import (
	"bufio"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ExampleConfig is bigdelete.json.example, built into the binary for -exampleconfig.
//
//go:embed bigdelete.json.example
var ExampleConfig string

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
	// Parallel is the default for ParallelExtract
	Parallel int `json:"parallel"`
	// ParallelExtract is how many tables extract their ROWIDs at the same time;
	// deletes always run one table at a time
	ParallelExtract   int    `json:"parallelExtract"`
	DeleteRowidFiles  string `json:"deleteRowidFiles"`
	CleanupRowidFiles bool   `json:"cleanupRowidFiles"`
	// PurgeRowidFilesOlderThan is an age like "5d", "78m" or "6d3m"; empty means never purge
	PurgeRowidFilesOlderThan string `json:"purgeRowidFilesOlderThan"`
	DeleteOnly               bool   `json:"deleteOnly"`
	// ExtractOnly does the same as the -extractonly flag
	ExtractOnly bool          `json:"extractOnly"`
	Tables      []TableConfig `json:"tables"`
	// ConnectString is used only when -connect is not given
	ConnectString string `json:"connectString"`

	purgeAge time.Duration
}

type TableConfig struct {
	Table   string  `json:"table"`
	SQL     string  `json:"sql"`
	SQLPre  sqlList `json:"sql-pre"`
	SQLPost sqlList `json:"sql-post"`
	Threads int     `json:"threads"`
	Commit  int     `json:"commit"`
	Group   int     `json:"group"`
	Enabled *bool   `json:"enabled"`
}

// sqlList holds sql-pre / sql-post statements; in JSON it may be a single
// string or an array of strings.
type sqlList []string

func (l *sqlList) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*l = sqlList{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return fmt.Errorf("sql-pre/sql-post must be a string or an array of strings")
	}
	*l = many
	return nil
}

// isEnabled reports whether the table should be processed; a table is enabled
// unless the config explicitly sets "enabled": false.
func (t TableConfig) isEnabled() bool {
	return t.Enabled == nil || *t.Enabled
}

func RunConfigFile(configFile string, checkConfig bool, parallel int, extractOnly bool) Progerr {
	cfg, perr := loadConfigFile(configFile)
	if perr.Err != nil {
		return perr
	}
	if checkConfig {
		return Progerr{Code: 0, Msg: "config file checked ok", Err: nil}
	}

	if extractOnly && cfg.DeleteOnly {
		return Progerr{fmt.Errorf("cannot use -extractonly with deleteOnly setting"), "cannot use -extractonly with deleteOnly setting", 21}
	}
	// extract-only is on when either the -extractonly flag or the config setting asks for it
	extractOnly = extractOnly || cfg.ExtractOnly

	// -parallel overrides parallelExtract
	extractPar := cfg.ParallelExtract
	if parallel > 0 {
		extractPar = parallel
	}
	extractPar = max(extractPar, 1)

	if ConnStr == "" {
		ConnStr = cfg.ConnectString
	}

	db, perr := connectDB()
	if perr.Err != nil {
		return perr
	}
	defer db.Close()

	dbID, perr := getDBIdent(db)
	if perr.Err != nil {
		return perr
	}
	log.Println("database", dbID.tag, "schema", dbID.schema)

	perr = createTempTable(db)
	if perr.Code == 0 {
		log.Println("temp table checked ok")
	} else {
		log.Println(perr.Msg)
		return perr
	}

	if cfg.purgeAge > 0 {
		if cfg.DeleteOnly {
			log.Println("purgeRowidFilesOlderThan ignored in delete-only mode")
		} else if perr = purgeRowidFiles(cfg.DeleteRowidFiles, cfg.purgeAge); perr.Err != nil {
			return perr
		}
	}

	log.Println("extracting up to", extractPar, "table(s) at a time; deletes run one table at a time after all extracts")
	return runTablePool(db, dbID, cfg, extractPar, extractOnly)
}

// dbIdent identifies the database a run is connected to, so that runs against
// different databases that have tables with the same name use different rowid
// files.
type dbIdent struct {
	tag    string // db_unique_name, plus the PDB name when connected to one
	schema string // current_schema, used to qualify unqualified table names
}

// getDBIdent asks the database who it is instead of parsing the connect
// string: the connect string can have many formats and holds the password, and
// two aliases for the same database correctly give the same tag.
func getDBIdent(db *sql.DB) (dbIdent, Progerr) {
	var id dbIdent
	var dbName, conName string

	err := db.QueryRow(`select sys_context('userenv','db_unique_name'), sys_context('userenv','current_schema') from dual`).Scan(&dbName, &id.schema)
	if err != nil {
		return id, Progerr{err, "cannot get database name", 3}
	}

	// con_name does not exist before 12c; there the database name alone is enough
	if err := db.QueryRow(`select sys_context('userenv','con_name') from dual`).Scan(&conName); err == nil &&
		conName != "" && !strings.EqualFold(conName, "CDB$ROOT") && !strings.EqualFold(conName, dbName) {
		dbName += "_" + conName
	}

	id.tag = strings.ToUpper(tableNameSanitizer.ReplaceAllString(dbName, "_"))
	id.schema = strings.ToUpper(id.schema)
	return id, Progerr{nil, "", 0}
}

// rowidFileName returns the rowid file name for a table, which is the same on
// every run: bigdelete-<DB>-<SCHEMA.TABLE>.rowids. purgeRowidFiles relies on
// the bigdelete-*.rowids shape.
func (id dbIdent) rowidFileName(table string) string {
	table = strings.ToUpper(strings.TrimSpace(table))
	if !strings.Contains(table, ".") {
		table = id.schema + "." + table
	}
	return "bigdelete-" + id.tag + "-" + tableNameSanitizer.ReplaceAllString(table, "_") + ".rowids"
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

	if cfg.ExtractOnly && cfg.DeleteOnly {
		return cfg, Progerr{fmt.Errorf("extractOnly and deleteOnly are both true"), "cannot use extractOnly with deleteOnly setting", 21}
	}

	if cfg.Parallel == 0 {
		cfg.Parallel = 1
	}
	if cfg.ParallelExtract == 0 {
		cfg.ParallelExtract = cfg.Parallel
	}

	if strings.TrimSpace(cfg.DeleteRowidFiles) == "" {
		cfg.DeleteRowidFiles = getTempDir()
	}

	if err := os.MkdirAll(cfg.DeleteRowidFiles, 0755); err != nil {
		return cfg, Progerr{err, "cannot create rowid directory " + cfg.DeleteRowidFiles, 22}
	}

	if cfg.PurgeRowidFilesOlderThan != "" {
		age, err := parseFileAge(cfg.PurgeRowidFilesOlderThan)
		if err != nil {
			return cfg, Progerr{err, "invalid purgeRowidFilesOlderThan: " + err.Error(), 9}
		}
		cfg.purgeAge = age
	}

	// each table has one rowid file, so an enabled table may appear only once
	seen := map[string]int{}
	for i, t := range cfg.Tables {
		if strings.TrimSpace(t.Table) == "" {
			return cfg, Progerr{fmt.Errorf("tables[%d]: missing \"table\"", i), fmt.Sprintf("tables[%d]: missing \"table\"", i), 9}
		}
		if t.isEnabled() {
			key := strings.ToUpper(strings.TrimSpace(t.Table))
			if j, dup := seen[key]; dup {
				msg := fmt.Sprintf("tables[%d] and tables[%d] are both enabled for table %s", j, i, t.Table)
				return cfg, Progerr{fmt.Errorf("%s", msg), msg, 9}
			}
			seen[key] = i
		}
		if t.isEnabled() && !cfg.DeleteOnly && strings.TrimSpace(t.SQL) == "" {
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

var fileAgeFormat = regexp.MustCompile(`^(?:(\d+)d)?(?:(\d+)m)?$`)

// parseFileAge parses an age given in days and/or minutes, like "5d", "78m" or
// "6d3m". A zero age is rejected because it would purge every rowid file.
func parseFileAge(s string) (time.Duration, error) {
	parts := fileAgeFormat.FindStringSubmatch(s)
	if parts == nil || s == "" {
		return 0, fmt.Errorf("%q is not an age like 5d, 78m or 6d3m", s)
	}

	var values [2]int64
	for i, part := range parts[1:] {
		if part == "" {
			continue
		}
		v, err := strconv.ParseInt(part, 10, 32)
		if err != nil {
			return 0, fmt.Errorf("%q is too large", s)
		}
		values[i] = v
	}

	minutes := values[0]*24*60 + values[1]
	if minutes == 0 {
		return 0, fmt.Errorf("%q must be greater than zero", s)
	}
	if minutes > math.MaxInt64/int64(time.Minute) {
		return 0, fmt.Errorf("%q is too large", s)
	}
	return time.Duration(minutes) * time.Minute, nil
}

// purgeRowidFiles removes rowid files older than maxAge from rowidDir, whichever
// table they belong to. Only regular files named like extractRowids names them
// are touched. A file that cannot be removed is logged but does not stop the run.
func purgeRowidFiles(rowidDir string, maxAge time.Duration) Progerr {
	entries, err := os.ReadDir(rowidDir)
	if err != nil {
		return Progerr{err, "cannot read rowid directory " + rowidDir, 23}
	}

	cutoff := time.Now().Add(-maxAge)
	for _, entry := range entries {
		if matched, _ := filepath.Match("bigdelete-*.rowids", entry.Name()); !matched || !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		path := filepath.Join(rowidDir, entry.Name())
		if err := os.Remove(path); err != nil {
			log.Println("cannot purge old rowid file", path, err)
			continue
		}
		log.Println("purged old rowid file", path)
	}
	return Progerr{nil, "", 0}
}

// runTablePool runs the enabled tables in two phases:
//
//  1. Extract: up to extractPar tables at a time run sql-pre and extract their
//     ROWIDs (in delete-only mode: only check the rowid file exists).
//  2. Delete: only after every extract has finished, and only if all of them
//     worked, the tables are deleted one at a time in config order, each with
//     its own threads, followed by its sql-post.
//
// A failed extract means no delete runs at all, because the tables of one
// config are usually meant to be deleted together. A failed delete stage
// (other than the worker os.Exit) is reported and the next table goes on.
func runTablePool(db *sql.DB, dbID dbIdent, cfg ConfigFile, extractPar int, extractOnly bool) Progerr {
	var tables []TableConfig
	for _, t := range cfg.Tables {
		if t.isEnabled() {
			tables = append(tables, t)
		}
	}
	paths := make([]string, len(tables))
	for i, t := range tables {
		paths[i] = filepath.Join(cfg.DeleteRowidFiles, dbID.rowidFileName(t.Table))
	}

	// phase 1: extracts; each worker writes only its own table's slot
	extractErrs := make([]Progerr, len(tables))
	work := make(chan int)
	var wg sync.WaitGroup
	wg.Add(extractPar)
	for w := 0; w < extractPar; w++ {
		go func() {
			defer wg.Done()
			for i := range work {
				extractErrs[i] = extractTable(db, tables[i], paths[i], cfg.DeleteOnly)
			}
		}()
	}
	for i := range tables {
		work <- i
	}
	close(work)
	wg.Wait()

	var errs []Progerr
	for _, perr := range extractErrs {
		if perr.Err != nil {
			errs = append(errs, perr)
		}
	}

	// phase 2: deletes, one table at a time, in config order
	switch {
	case extractOnly:
	case len(errs) > 0:
		log.Println("extract failed for", len(errs), "of", len(tables), "table(s); no deletes run")
	default:
		// a failed delete stops the run, so no later table is deleted; a failed
		// sql-post is only reported, because that table's rows are already gone
		for i, t := range tables {
			if perr := deleteTable(db, t, paths[i], cfg.CleanupRowidFiles && !cfg.DeleteOnly); perr.Err != nil {
				errs = append(errs, perr)
				var rest []string
				for _, r := range tables[i+1:] {
					rest = append(rest, r.Table)
				}
				if len(rest) > 0 {
					log.Println("delete failed for", t.Table, "; not deleted:", strings.Join(rest, ", "))
				}
				break
			}
			if perr := runTableSQL(db, t.Table, "sql-post", t.SQLPost, 29); perr.Err != nil {
				log.Printf("%s: %v; continuing with the next table", perr.Msg, perr.Err)
				errs = append(errs, perr)
			}
		}
	}

	var msgs []string
	code := 0
	for _, perr := range errs {
		log.Printf("table run failed: %s: %v", perr.Msg, perr.Err)
		msgs = append(msgs, fmt.Sprintf("%s: %v", perr.Msg, perr.Err))
		code = perr.Code
	}
	if len(msgs) > 0 {
		return Progerr{fmt.Errorf("%d of %d table(s) failed", len(msgs), len(tables)), strings.Join(msgs, "; "), code}
	}
	return Progerr{nil, "", 0}
}

// extractTable is a table's extract phase: sql-pre, then the ROWID extract, or
// in delete-only mode a check that the rowid file from an earlier extract exists.
func extractTable(db *sql.DB, t TableConfig, rowidPath string, deleteOnly bool) Progerr {
	log.Println("running table " + t.Table)

	if perr := runTableSQL(db, t.Table, "sql-pre", t.SQLPre, 28); perr.Err != nil {
		return perr
	}
	if deleteOnly {
		return checkRowidFile(rowidPath, t.Table)
	}
	return extractRowids(db, t, rowidPath)
}

// deleteTable deletes the ROWIDs in rowidPath from the table (sql-post is run
// by the caller). With cleanup the rowid file is removed, but only after a
// successful delete, so a failed one can be rerun in delete-only mode.
func deleteTable(db *sql.DB, t TableConfig, rowidPath string, cleanup bool) Progerr {
	log.Println("deleting from table " + t.Table)

	f, err := os.Open(rowidPath)
	if err != nil {
		return Progerr{err, "cannot open rowid file for " + t.Table, 17}
	}
	perr := runDelete(db, t.Table, t.Threads, t.Commit, t.Group, f)
	f.Close()
	if perr.Err != nil {
		return perr
	}

	if cleanup {
		if DebugFlag {
			log.Println("table", t.Table, "removing rowid file", rowidPath)
		}
		os.Remove(rowidPath)
	} else if DebugFlag {
		log.Println("table", t.Table, "keeping rowid file", rowidPath)
	}
	return Progerr{nil, "", 0}
}

// runTableSQL executes a table's optional sql-pre / sql-post statements in
// order, stopping at the first failure; empty entries are skipped. Plain SQL
// must not end in ";", PL/SQL blocks must.
func runTableSQL(db *sql.DB, tableName, kind string, stmts sqlList, code int) Progerr {
	for i, stmt := range stmts {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		label := fmt.Sprintf("%s[%d]", kind, i)
		log.Println("table", tableName, "running", label)
		if DebugFlag {
			log.Println("table", tableName, label+":", stmt)
		}
		if _, err := db.Exec(stmt); err != nil {
			return Progerr{err, label + " failed for " + tableName, code}
		}
	}
	return Progerr{nil, "", 0}
}

var tableNameSanitizer = regexp.MustCompile(`[^A-Za-z0-9_.]+`)

// extractRowids runs the table's ROWID SQL and writes the result to rowidPath,
// replacing any file from an earlier run. The rows go to a temporary file in
// the same directory first, renamed at the end, so a failed or interrupted
// extract never leaves a partial file under the real name. The temporary name
// ends in .tmp, so purgeRowidFiles leaves it alone.
func extractRowids(db *sql.DB, t TableConfig, rowidPath string) Progerr {
	rows, err := db.Query(t.SQL)
	if err != nil {
		return Progerr{err, "rowid extraction query failed for " + t.Table, 13}
	}
	defer rows.Close()

	tmp, err := os.CreateTemp(filepath.Dir(rowidPath), filepath.Base(rowidPath)+".*.tmp")
	if err != nil {
		return Progerr{err, "cannot create rowid temp file for " + t.Table, 17}
	}
	tmpPath := tmp.Name()
	fail := func(err error, msg string, code int) Progerr {
		tmp.Close()
		os.Remove(tmpPath)
		return Progerr{err, msg + " for " + t.Table, code}
	}

	w := bufio.NewWriter(tmp)
	var rid string
	var n int
	for rows.Next() {
		if err := rows.Scan(&rid); err != nil {
			return fail(err, "rowid scan failed", 16)
		}
		if _, err := w.WriteString(rid + "\n"); err != nil {
			return fail(err, "cannot write rowid temp file", 18)
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return fail(err, "rowid extraction failed", 19)
	}
	if err := w.Flush(); err != nil {
		return fail(err, "cannot flush rowid temp file", 18)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return Progerr{err, "cannot close rowid temp file for " + t.Table, 18}
	}
	if err := os.Rename(tmpPath, rowidPath); err != nil {
		os.Remove(tmpPath)
		return Progerr{err, "cannot rename rowid temp file to " + rowidPath, 18}
	}

	log.Println("table", t.Table, "extracted", n, "rowids to", rowidPath)
	return Progerr{nil, "", 0}
}

// checkRowidFile checks, in delete-only mode, that the table's rowid file
// from an earlier extract exists.
func checkRowidFile(rowidPath string, tableName string) Progerr {
	info, err := os.Stat(rowidPath)
	if os.IsNotExist(err) {
		return Progerr{err, "no rowid file " + rowidPath + " for " + tableName, 24}
	}
	if err != nil {
		return Progerr{err, "cannot check rowid file " + rowidPath + " for " + tableName, 23}
	}
	if !info.Mode().IsRegular() {
		return Progerr{fmt.Errorf("%s is not a regular file", rowidPath), "rowid file " + rowidPath + " for " + tableName + " is not a regular file", 24}
	}
	return Progerr{nil, "", 0}
}
