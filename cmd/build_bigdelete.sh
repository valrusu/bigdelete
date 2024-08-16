# needs to be compiled on Linux due to the godror dependency
# maybe on sandag app server 172.22.5.85 as etctrx
set -e
set -o pipefail

if [[ "$OS" =~ "Windows" ]] ; then
    rm -f bigdelete.exe
    go build -o bigdelete.exe bigdelete.go && {
        echo "Windows build ok; rebuild on Linux"
        rm -f bigdelete.exe
    }
else
    rm -f bigdelete
    go build -o bigdelete bigdelete.go
fi
