# Sanctioned revenue computation. The body is executed by the python sidecar,
# never in this process; the sidecar echoes its SHA-256 so the attester can
# prove which body produced the rows.
import json
import sys


def run(year: int) -> dict:
    return {"revenue_ytd": 1250, "year": year}


if __name__ == "__main__":
    print(json.dumps(run(int(sys.argv[1]))))
