# bigdelete
bigdelete handles lots of deletes from and Oracle table

When the amount of data to be deleted from an Oracle table gets really big, the usual approaches may not work anymore:
- simple DELETE statetemt with or without a WHERE clause: may cause huge UNDO usage, table scans which take forever etc
- "create table as select" from the original table and rename the new and old tables: not an online operation
- TODO online move table, with or without a WHERE clause
- TODO describe other mothods I tried and why they failed

With bigdelete I am following this approach:
- generate a list of ROWIDs to be deleted from a table; save them in a file
- call bigdelete, piping the list of ROWIDs to it.
- bigdelete opens a number of parallel sessions (the -threads parameter) and in each one deletes N ROWIDs at a time (the -commit parameter), using the ROWIDs from its standard input, until all are deleted.
- the process for each sessions is:
  - insert N ROWIDs into a temp table (called bigdeletemp)
  - run "delete from target_table where rowid in (select rid from bigdeletetemp)
  - commit
  - repeat until no more ROWIDs read in stdin

I needed the operation to be totally online, just as if someone would have deleted the records
- Note that the list of ROWIDs could be generated on the fly (maybe with a script calling sqlplus) and then have this output piped to bigdelete. However this will likely be a problem because the cursor used to extract the ROWIDs will still be open when data will start to be deleted, and this will be very slow because of the UNDO needed for keeping this cursor consistent and for the delete itself.
