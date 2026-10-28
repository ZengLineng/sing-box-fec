import ctypes
import os
import platform
import struct
import sys
from pathlib import Path


def require_filters(counts):
    if len(counts) != 2 or min(counts) < 1:
        raise RuntimeError("FECBox requires effective ingress and egress cgroup IP filters")


if "--self-test" in sys.argv:
    require_filters([1, 1])
    for counts in ([0, 1], [1, 0], []):
        try:
            require_filters(counts)
        except RuntimeError:
            continue
        raise AssertionError("Missing IP filter accepted")
    sys.exit(0)

# shortcut: Linux x86_64 BPF ABI only; verify syscall numbers before adding another server architecture.
if platform.system() != "Linux" or platform.machine() != "x86_64":
    raise RuntimeError("Unverified BPF query ABI; refusing to start")
cgroup = next(line.split(":", 2)[2] for line in Path("/proc/self/cgroup").read_text().splitlines() if line.startswith("0::"))
fd = os.open("/sys/fs/cgroup" + cgroup, os.O_RDONLY | os.O_DIRECTORY)
libc = ctypes.CDLL(None, use_errno=True)
counts = []
try:
    for attach_type in (0, 1):
        attr = ctypes.create_string_buffer(128)
        struct.pack_into("IIIIQII", attr, 0, fd, attach_type, 1, 0, 0, 0, 0)
        ctypes.set_errno(0)
        result = libc.syscall(321, 16, ctypes.byref(attr), 128)
        error = ctypes.get_errno()
        if result != 0 and error != 28:
            raise OSError(error, "Could not query effective cgroup IP filters")
        counts.append(struct.unpack_from("I", attr.raw, 24)[0])
finally:
    os.close(fd)
require_filters(counts)
print("Effective ingress/egress cgroup IP filters present", flush=True)
