#! /usr/bin/env python3
##
import os
import sys

BLOCKSIZE = 128 * 1000


def rewrite_file(path):
    # Get size of file
    size = os.path.getsize(path)

    # Iterate through file
    with open(path, "rb+") as file:
        # Iterate through steps of block size
        for i in range(0, size, BLOCKSIZE):
            # Seek to offset and read
            file.seek(i)
            a, b = file.read(2)

            # Seek back to offset and write
            file.seek(i)
            file.write(bytes([a, b]))


def main():
    rewrite_file(sys.argv[1])


if __name__ == "__main__":
    main()
