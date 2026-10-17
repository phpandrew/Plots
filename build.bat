@echo off
rem Local build: version comes from the VERSION file, stamped as <VERSION>-dev
set /p VER=<VERSION
go build -trimpath -ldflags "-s -w -H windowsgui -X main.version=%VER%-dev" -o Plots.exe . && echo Built Plots %VER%-dev
