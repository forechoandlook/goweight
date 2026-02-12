![](media/cover.png)

# goweight

A tool to analyze and troubleshoot a Go binary size.

For more, see [this blog post](https://medium.com/@jondot/a-story-of-a-fat-go-binary-20edc6549b97#.bzaq4nol0)

✅ Get a breakdown of all modules inside a static-linked binary  
✅ Supports Go 1.11+ modules  
✅ Output as JSON for tracking and/or monitoring as part of CI  
✅ Analyze existing binaries  
✅ Experimental build-process analysis  

## Quick Start

### With Go Modules - Go 1.11 or higher

```
$ go get github.com/jondot/goweight
$ cd current-project
$ goweight
```

### Without Go Modules - Before Go 1.11

```
$ git clone https://github.com/jondot/goweight
$ cd goweight
$ go install

$ cd current-project
$ goweight
```


As an example, here's what `goweight` has to say about itself:

```
$ ./goweight
284 kB github.com/thoas
 216 kB github.com/alecthomas
  66 kB github.com/dustin
  20 kB github.com/xhit
   0 B github.com/jondot
```

## Features

### Static-Linked Binary Analysis (Default)
Analyzes the final statically-linked binary to determine the actual contribution of each dependency to the final binary size.

### Existing Binary Analysis
Analyze an already-built binary file:
```
$ goweight -b /path/to/binary
```

### Verbose Mode
Show detailed breakdown of all packages:
```
$ goweight -v
```

### JSON Output
Get machine-readable output:
```
$ goweight -j
```

### Build Process Analysis (Experimental)
Analyze the build process to see compilation sizes (note: due to Go's temporary work directory mechanism, this currently shows package names but not actual sizes):
```
$ goweight --build-analysis
```

### Additional Options
- Use build tags: `--tags="prod,debug"`
- Specify packages: `goweight ./cmd/app`

## How It Works

goweight uses multiple approaches to analyze Go binaries:

1. **Static Linking Analysis**: Builds the project and analyzes the final binary using debug information and symbol tables
2. **Binary Analysis**: Reads existing binaries using `debug/buildinfo` and binary format parsers
3. **Build Process Analysis**: Parses Go compiler's build output (experimental)

## Documentation

For detailed usage instructions and advanced options, see [USAGE.md](USAGE.md).

### Thanks:

To all [Contributors](https://github.com/jondot/goweight/graphs/contributors) - you make this happen, thanks!

# Copyright

Copyright (c) 2018-2026 [@jondot](http://twitter.com/jondot). See [LICENSE](LICENSE.txt) for further details.
