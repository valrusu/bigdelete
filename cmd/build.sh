
go build -C bigdelete

if test -z "$WINDIR" ; then
	echo Build OK
	gzip -c bigdelete/bigdelete >bigdelete/bigdelete.gz
	cksum bigdelete/bigdelete bigdelete/bigdelete.gz
else
	echo Build OK, need to compile on Linux
	rm -fv bigdelete/bigdelete.exe
fi
