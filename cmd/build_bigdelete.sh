# needs to be compiled on Linux due to the godror dependency
set -e
set -o pipefail

if [[ "$OS" =~ "Windows" ]] ; then
    rm -f bigdelete.exe
    go build -o bigdelete.exe bigdelete.go && {
        echo "Windows build ok; rebuild on Linux"
        rm -f bigdelete.exe
    }
else
    # godror embeds ODPI-C, so CGO (a C compiler) is required at build time;
    # without one, go silently sets CGO_ENABLED=0 and the build fails with
    # misleading "undefined: VersionInfo" errors in vendor/godror
    if [[ "$(go env CGO_ENABLED)" != "1" ]] ; then
        echo "error: CGO is disabled (go env CGO_ENABLED = $(go env CGO_ENABLED))." >&2
        echo "godror needs a C compiler at build time - install gcc and retry." >&2
        exit 1
    fi
    rm -f bigdelete
    go build -o bigdelete bigdelete.go
fi
