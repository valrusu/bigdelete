if test -z "$WINDIR" ; then # linux
	rm -f bigdelete bigdelete.gz || true
	go build && gzip -c bigdelete >bigdelete.gz && cksum bigdelete.gz
else # windows
	go build && echo OK && rm bigdelete.exe
fi
exit

date
EXEL=dataarchive
EXEW=EXEL.exe
rm -fv ${EXEL} ${EXEW}
go build -o ${EXE}.exe && echo windows build - ok && rm -fv ${EXE}.exe
rm -fv ${EXE} ${EXE}.gz
GOOS=linux GOARCH=amd64 go build -o ${EXE}
test -f ${EXE} && test "$1" == "push" && {
	gzip -c ${EXE} >${EXE}.gz && 
	echo linux build - ok && 
	cp -fv ${EXE}.gz /c/Users/rusuvale/kapsch/temp/ &&
	cksum ${EXE}.gz

	#echo fredex sit db
	#scp ${EXE}.gz etctrx@10.120.20.10:/home/etctrx/val/dataarchive/ && ssh etctrx@10.120.20.10 'cd /home/etctrx/val/dataarchive/ && gzip -dfv dataarchive.gz && chmod +x dataarchive'
}
