#!/bin/sh
# Builds the stub host libraries and the fakefabric-check client.
set -eu
H=/out/host/usr/lib/brics-test
C=/out/client
mkdir -p "$H" "$C/usr/local/bin" "$C/usr/lib" "$C/opt/masked"
gcc -shared -fPIC -DFAKEDEP_VARIANT='"host"' -Wl,-soname,libfakedep.so.1 -o "$H/libfakedep.so.1.0.0" /src/fakedep.c
ln -s libfakedep.so.1.0.0 "$H/libfakedep.so.1"
gcc -shared -fPIC -Wl,-soname,libfakefabric.so.1 -o "$H/libfakefabric.so.1.0.0" /src/fakefabric.c -L"$H" -l:libfakedep.so.1
ln -s libfakefabric.so.1.0.0 "$H/libfakefabric.so.1"
# The image's own libfakedep lives only in /usr/lib, a trusted directory not
# listed in ld.so.conf: the case module-hook's conf ordering must handle.
gcc -shared -fPIC -DFAKEDEP_VARIANT='"image"' -Wl,-soname,libfakedep.so.1 -o "$C/usr/lib/libfakedep.so.1" /src/fakedep.c
gcc -o "$C/usr/local/bin/fakefabric-check" /src/check.c -L"$H" -l:libfakefabric.so.1 -Wl,-rpath-link,"$H"
echo secret > "$C/opt/masked/secret"
# libfakelate lives outside ld.so.conf: the readiness e2e adds its directory
# to prove pods wait for a host module to become ready.
L=/out/host/usr/lib/brics-late
mkdir -p "$L"
gcc -shared -fPIC -DFAKEDEP_VARIANT='"late"' -Wl,-soname,libfakelate.so.1 -o "$L/libfakelate.so.1.0.0" /src/fakedep.c
ln -s libfakelate.so.1.0.0 "$L/libfakelate.so.1"
