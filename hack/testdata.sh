#!/usr/bin/env bash
# Runs inside debian:bookworm (see gen-testdata.sh). Writes ELF and
# ld.so.cache fixtures under /out (= internal/ in the repo).
set -euo pipefail
apt-get update -qq >/dev/null && apt-get install -y -qq gcc libc6-dev >/dev/null
S=/src
H=/out/discovery/testdata/hostroot
L=$H/usr/lib/brics-test
rm -rf /out/discovery/testdata /out/hook/testdata
mkdir -p "$L/sub" "$L/rp" "$H/etc" /out/hook/testdata/libc231 /tmp/gone
so() { gcc -shared -fPIC -o "$1" -Wl,-soname,"$2" "${@:3}"; }

so "$L/libfakedep.so.1.0.0" libfakedep.so.1 "$S/fakedep.c"
ln -s libfakedep.so.1.0.0 "$L/libfakedep.so.1"
so "$L/libfakefabric.so.1.0.0" libfakefabric.so.1 "$S/fakefabric.c" -L"$L" -l:libfakedep.so.1
ln -s libfakefabric.so.1.0.0 "$L/libfakefabric.so.1"
# RUNPATH=$ORIGIN/sub; libsubdep is not in the cache, only in sub/.
so "$L/sub/libsubdep.so.1" libsubdep.so.1 "$S/fakedep.c"
so "$L/liborigin.so.1" liborigin.so.1 "$S/originuser.c" -Wl,--enable-new-dtags,-rpath,'$ORIGIN/sub' -L"$L/sub" -l:libsubdep.so.1
# RPATH (old dtags)=$ORIGIN/rp; librpdep is only in rp/.
so "$L/rp/librpdep.so.1" librpdep.so.1 "$S/fakedep.c"
so "$L/librpath.so.1" librpath.so.1 "$S/originuser.c" -Wl,--disable-new-dtags,-rpath,'$ORIGIN/rp' -L"$L/rp" -l:librpdep.so.1
# A library whose dependency is not installed.
so /tmp/gone/libgone.so.1 libgone.so.1 "$S/fakedep.c"
so "$L/libneedsgone.so.1" libneedsgone.so.1 "$S/originuser.c" -L/tmp/gone -l:libgone.so.1

# A library whose DT_NEEDED is a path, not a soname: linking a library that
# has no soname by relative path records the path as given.
mkdir -p /tmp/w && gcc -shared -fPIC -o /tmp/w/libweird.so "$S/fakedep.c"
(cd /tmp && gcc -shared -fPIC -o "$L/libpathdep.so.1" -Wl,-soname,libpathdep.so.1 "$S/originuser.c" w/libweird.so)
echo /usr/lib/brics-test > "$H/etc/ld.so.conf"
ldconfig -X -r "$H" -f /etc/ld.so.conf -C /etc/ld.so.cache
dpkg --print-architecture > "$H/ARCH"

# Fake glibc 2.31 for the hook's version check.
gcc -shared -fPIC -nostdlib -o /out/hook/testdata/libc231/libc.so.6 -Wl,-soname,libc.so.6 \
	-Wl,--version-script,"$S/fakelibc.map" "$S/fakelibc.c"
chown -R "${HOST_UID:-0}:${HOST_GID:-0}" /out/discovery/testdata /out/hook/testdata
