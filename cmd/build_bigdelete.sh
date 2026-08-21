# needs to be compiled on Linux due to the godror dependency
set -e
set -o pipefail

if [[ "$OS" =~ "Windows" ]] ; then
    rm -f bigdelete.exe
    go build -o bigdelete.exe bigdelete.go && {
        echo "Windows build ok; rebuild on Linux"
        rm -f bigdelete.exe
    }
    exit
fi

if [[ "$(go env CGO_ENABLED)" != "1" ]] ; then
    echo "error: CGO is disabled (go env CGO_ENABLED = $(go env CGO_ENABLED))." >&2
    echo "godror needs a C compiler at build time - install gcc and retry." >&2
    exit 1
fi

if [[ "$(grep VERSION= /etc/os-release | head -n 1 | cut -f 2 -d "=" | tr -d '"')" == "7.9" ]] ; then
    rm -f bigdelete_oel7
    go build -o bigdelete_oel7 bigdelete.go
elif [[ "$(grep VERSION= /etc/os-release | head -n 1 | cut -f 2 -d "=" | tr -d '"')" == "9.8" ]] ; then
    rm -f bigdelete_oel9
    go build -o bigdelete_oel9 bigdelete.go
else
    # godror embeds ODPI-C, so CGO (a C compiler) is required at build time;
    # without one, go silently sets CGO_ENABLED=0 and the build fails with
    # misleading "undefined: VersionInfo" errors in vendor/godror
    rm -f bigdelete
    go build -o bigdelete bigdelete.go
fi
