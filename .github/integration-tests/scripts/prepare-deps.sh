#!/bin/bash

# Script to prepare local dependencies for Docker build

set -e

echo "Preparing local dependencies for Docker build..."

# Check if we're in the right directory
if [ ! -f "go.mod" ]; then
    echo "Error: go.mod not found. Run this script from the gm/ directory."
    exit 1
fi

# Create deps directory
mkdir -p deps

# Copy local dependencies
if [ -d "../ev-abci" ]; then
    echo "Copying ev-abci..."
    cp -r ../ev-abci deps/
else
    echo "Warning: ../ev-abci not found"
fi

if [ -d "../ev-node" ]; then
    echo "Copying ev-node..."
    cp -r ../ev-node deps/
else
    echo "Warning: ../ev-node not found"
fi

if [ -d "../tastora" ]; then
    echo "Copying tastora..."
    cp -r ../tastora deps/
else
    echo "Warning: ../tastora not found"
fi

echo "Dependencies prepared in deps/ directory"