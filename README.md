# bigdelete
bigdelete handles lots of deletes from an Oracle table

When the amount of data to be deleted from an Oracle table gets really big, the usual approaches may not work anymore:
- simple DELETE statetemt with or without a WHERE clause: may cause huge UNDO usage, table scans which take forever etc
- "create table as select" from the original table and rename the new and old tables: not an online operation
- online move table, with or without a WHERE clause
- partition pruning - from 12c seems to work fine with global indexes too, but it requires ... partitioning option (license) and to really have the tables partitioned using the same criteria used for data purging
- cursor on original table + delete = consistency issues

With bigdelete I am following this approach:
- generate a list of ROWIDs to be deleted from a table; save them in a file to avoid the need for the DB to maintain consistency
- call bigdelete, piping the list of ROWIDs to it.
- bigdelete opens a number of parallel sessions (the -threads parameter) and in each one deletes N ROWIDs at a time (the -commit parameter), using the ROWIDs from its standard input, until all are deleted.
- the process for each sessions is:
  - insert N ROWIDs into a temp table (called bigdeletemp)
  - run "delete from target_table where rowid in (select rid from bigdeletetemp).
    By using the temp table we will have a single SQL query parsed.
  - commit
  - repeat until no more ROWIDs read in stdin

I needed the operation to be totally online, just as if someone would have deleted the records
- Note that the list of ROWIDs could be generated on the fly (maybe with a script calling sqlplus) and then have this output piped to bigdelete. However this will likely be a problem because the cursor used to extract the ROWIDs will still be open when data will start to be deleted, and this will be very slow because of the UNDO needed for keeping this cursor consistent and for the delete itself.

Version history information:

version 1:
	delete from table where rowid in (a,b,c...), max 1000

	executed in N sessions

	SQLs are not reused - lots of hard parses

version 2:
	prepare insert statement

	truncate table bigdeletetemp - if not temp table with delete on commit
	insert into bigdeletetemp values (a)
	insert into bigdeletetemp values (b)
	insert into bigdeletetemp values (c)
	...
	commit (?)
	start transaction
	delete from table where rowid in (select rid from bigdeletetemp)
	commit transaction

	executed in N sessions

	SQLs are reused

	bigdeletetemp table is hardcoded - it can be a synonym to any table with the structure:
drop table bigdeletetemp;
create global temporary table bigdeletetemp (rid rowid) on commit delete rows;

DB transactions in Go:		
https://go.dev/doc/database/execute-transactions
