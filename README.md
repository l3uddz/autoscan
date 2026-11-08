# Autoscan

Autoscan replaces the default behavior of Plex and Emby for picking up file changes on the file system.
Autoscan integrates with Sonarr, Radarr, Readarr, and Lidarr.

Autoscan is a rewrite of the original [Plex Autoscan](https://github.com/l3uddz/plex_autoscan) in the Go language.
The rewrite has a more modular design that is easy to extend.

## Comparison to Plex Autoscan

Autoscan does not rely on manual trash deletion in Plex.
Thus, turn the `Empty trash automatically after every scan` setting in Plex back on.

Autoscan also adds these features to [Plex Autoscan](https://github.com/l3uddz/plex_autoscan):

- Autoscan supports Plex music libraries.
- Autoscan supports Emby and Jellyfin.
- Autoscan can send _scans_ to multiple Plex, Emby, and Jellyfin servers.

## Installing autoscan

Each official release has [pre-compiled binaries](https://github.com/l3uddz/autoscan/releases/latest) for Linux and macOS.
A [Docker image](#docker) is also available.

You can also build the Autoscan binary yourself.
Before you build it, make sure that your machine runs Linux, macOS, or WSL2.
Also make sure that [Go](https://golang.org/doc/install) 1.26 or later is installed.

To build the binary, do these steps in a terminal:

1. Clone this repository.
2. Go to the root directory of the repository.
3. Run `go build -o autoscan ./cmd/autoscan`.

The build makes a binary with the name `autoscan` in the root directory of the project.
To start Autoscan, run `./autoscan`.
To make Autoscan available from all directories, move the binary to `/bin` or `/usr/local/bin`.

To debug Autoscan, add the `-v` flag for debug mode.
For more detail about the internal behavior, add the `-vv` flag for trace mode.

## Overview

Autoscan has three modules:

- Triggers
- Processor
- Targets

### Rewriting paths

Triggers, targets, and the processor can each see a file at a different path.
For example, some of them run in Docker containers and others run on the host operating system (OS).

Rewrite rules solve this problem.
A trigger rule translates a path from the view of the trigger to the view of Autoscan.
A target rule translates a path from the view of Autoscan to the view of the target.

Before you begin, make sure that you understand regular expressions. \
Also make sure that you understand capture groups, because the `to` field uses them.

Triggers can receive paths from any source, for example a remote server, a Docker container, or the local file system.
You can set the `rewrite` field for each trigger.
This field can contain multiple rules.
Start each rule with a `-`.
The `from` field is a regular expression (regexp) that matches the path as the trigger sees it.
The `to` field converts that path into a path that is local to Autoscan.

Targets work the other way around.
A target converts the path that is local to Autoscan into a path that the target understands.
The target can be a Docker container, a remote server, or a different system.
The `from` field is a regexp that matches the path as Autoscan sees it.
The `to` field converts that path into a path that is local to the target.

All three modules must have access to each file.
A file from a trigger must be available to the processor and to all targets.

#### Simple example

This example uses this setup:

- Sonarr runs in a Docker container. The same example works for Lidarr, Radarr, and Readarr.
- Autoscan runs on the host OS, not in a container.
- Plex runs in a Docker container.

The configuration below only defines rewrite rules. Do not use it as it is.

```yaml
triggers:
  sonarr:
    - rewrite:
        # /tv contains folders with tv shows
        # This path is used within the Sonarr Docker container
        - from: /tv/

          # /mnt/unionfs/Media/TV links to the same folder, though from the host OS
          # This folder is accessed by Autoscan
          to: /mnt/unionfs/Media/TV/

targets:
  plex:
    - rewrite:
        # Same folder as above, accessible by Autoscan.
        # Note how we strip the "TV" part,
        # as we want both Movies and TV.
        - from: /mnt/unionfs/Media/

          # This path is used within the Plex Docker container
          to: /data/
```

This is the journey of the path `/tv/Westworld/Season 1/s01e01.mkv` from Sonarr:

1. The Sonarr trigger takes the folder of the file, `/tv/Westworld/Season 1`.
2. The trigger translates the folder to a path that is local to Autoscan. \
   `/mnt/unionfs/Media/TV/Westworld/Season 1`
3. Autoscan adds the folder to its datastore.
4. Autoscan translates its own path to a path that is local to Plex. \
   `/data/TV/Westworld/Season 1`

## Triggers

Triggers are the input of Autoscan.
They translate incoming data into a common data format, the Scan.
A Scan is a folder path with a priority.

Autoscan supports these triggers:

- Inotify: This trigger listens for changes on the file system. \
  Do not use this trigger on top of RClone mounts. \
  _This trigger can still contain bugs._

- Manual: This trigger scans a path that you send to it.

- The -arrs: Lidarr, Sonarr, Radarr, and Readarr. \
  These triggers receive webhooks from Lidarr, Sonarr, Radarr, and Readarr.

All triggers support these features:

- Trigger-wide priority: The processor processes higher priorities sooner. \
  _The default priority is 0._

- Rewrite rules based on regular expressions: These rules translate a path from the trigger to a path on the local file system. \
  _The paths can be identical on the trigger and on the local file system. In that case, you do not need the `rewrite` field._

### Manual

You can open `/triggers/manual` in a browser to submit requests manually.

Autoscan also has a `manual` webhook for custom scripts and for software that Autoscan does not support directly.
The manual endpoint is at `/triggers/manual`.

The manual endpoint accepts one or more directory paths.
Give each path in a `dir` query parameter.
If the configuration file sets a username and a password in the `authentication` option, the manual webhook uses basic authentication.
The other webhooks use the same authentication.

URL template: `POST /triggers/manual?dir=$path1&dir=$path2`

The following curl command asks Autoscan to scan the directories `/test/one` and `/test/two`:

```bash
curl --request POST \
  --url 'http://localhost:3030/triggers/manual?dir=%2Ftest%2Fone&dir=%2Ftest%2Ftwo' \
  --header 'Authorization: Basic aGVsbG8gdGhlcmU6Z2VuZXJhbCBrZW5vYmk='
```

### The -arrs

You can configure more than one HTTP trigger of the same type.
In that case, each trigger MUST have a `name` field with a unique value.
Autoscan uses the name to create the route `/triggers/:name`.

Autoscan provides these -arrs:

- Lidarr
- Radarr
- Readarr
- Sonarr

#### Connecting the -arrs

To add your webhook to Sonarr, Radarr, Readarr, or Lidarr, do these steps:

1. Open the `settings` page in Sonarr, Radarr, Readarr, or Lidarr.
2. Select the `connect` tab.
3. Click the large plus sign.
4. Select `webhook`.
5. Set the name to `Autoscan`, or to a different name that you prefer.
6. Select `On Import` and `On Upgrade`.
7. Set the URL to the Autoscan URL, followed by `/triggers/:name`. Here, `:name` is the name of the trigger in the Autoscan configuration.
8. Optional: Set the username and the password.

#### The latest events

Autoscan also supports these events in the latest versions of Radarr and Sonarr:

- `Rename`
- `On Movie Delete` and `On Series Delete`
- `On Movie File Delete` and `On Episode File Delete`

We do not know for certain that these three events cover all possible file system changes.
For now, keep using Bernard or the Inotify trigger to get all scans.

### Configuration

This part of the `config.yml` file shows the possible configuration of the triggers.
You can combine the triggers in the way that you like:

```yaml
# Optionally, protect your webhooks with authentication
authentication:
  username: hello there
  password: general kenobi

# port for Autoscan webhooks to listen on
port: 3030

triggers:
  # The manual trigger is always enabled, the config only adjusts its priority and the rewrite rules.
  manual:
    priority: 5
    rewrite:
      - from: ^/Media/
        to: /mnt/unionfs/Media/

  inotify:
    - priority: 0

      # filter with regular expressions
      include:
        - ^/mnt/unionfs/Media/
      exclude:
        - '\.(srt|pdf)$'

      # rewrite inotify path to unified filesystem
      rewrite:
        - from: ^/mnt/local/Media/
          to: /mnt/unionfs/Media/

      # local filesystem paths to monitor
      paths:
        - path: /mnt/local/Media

  lidarr:
    - name: lidarr # /triggers/lidarr
      priority: 1

  radarr:
    - name: radarr # /triggers/radarr
      priority: 2
    - name: radarr4k # /triggers/radarr4k
      priority: 5

  readarr:
    - name: readarr # /triggers/readarr
      priority: 1

  sonarr:
    - name: sonarr-docker # /triggers/sonarr-docker
      priority: 2

      # Rewrite the path from within the container
      # to your local filesystem.
      rewrite:
        - from: /tv/
          to: /mnt/unionfs/Media/TV/
```

## Processor

Triggers send the Scans that they receive to the processor.
The processor then saves the Scans to its datastore.

_The processor uses SQLite as its datastore._

In a separate process, the processor selects Scans from the datastore.
It always groups the files in the same folder together.
It waits until all files in that folder are older than the `minimum-age` value.
The default value is 10 minutes.

After all files are older than the minimum age, the processor calls all configured targets in parallel.
Each target gets a request to scan the folder.

### Anchor files

An anchor file is a file that shows that a remote mount is online.
You can define a list of `anchor files`.
The processor then does not call targets while a remote mount is offline.

Anchor files have no special properties and often have no content.
Autoscan only uses them to see whether a file exists on the file system.
Assume that you did not change an anchor file and the file is missing.
Then the remote mount is offline or the software has a problem.

If an anchor file is not available, the processor stops its operations until the file is available again.

If you merge multiple remote mounts with a tool such as [UnionFS](https://unionfs.filesystems.org) or [MergerFS](https://github.com/trapexit/mergerfs), use different anchor file names.
Each remote mount MUST have its own anchor file and its own name for that anchor file.
Also, give the merged path to the file, not the path on the remote mount.
This also shows whether the union software works correctly.

### Minimum age

Autoscan does not make sure that the files in scan requests from triggers exist on the file system.
To make sure that a file exists before it reaches the targets, set a minimum age.
The minimum age delays a Scan after a trigger adds it to the queue.
Then the processor sends the Scan to the targets.
The default minimum age is 10 minutes, which prevents common synchronization problems.

### Customising the processor

The processor lets you set the minimum age of a Scan.
You can also define a list of anchor files.

A part of the `config.yml` file:

```yaml
# override the minimum age to 30 minutes:
minimum-age: 30m

# override the delay between processed scans:
# defaults to 5 seconds
scan-delay: 15s

# override the interval scan stats are displayed:
# defaults to 1 hour / 0s to disable
scan-stats: 1m

# set multiple anchor files
anchors:
  - /mnt/unionfs/drive1.anchor
  - /mnt/unionfs/drive2.anchor
```

The `minimum-age`, `scan-delay`, and `scan-stats` fields take a string in this format:

- `1s` sets the value to 1 second.
- `5m` sets the value to 5 minutes.
- `1m30s` sets the value to 1 minute and 30 seconds.
- `1h` sets the value to 1 hour.

_Do not forget the `s`, `m`, or `h` suffix. Without a suffix, the time unit is nanoseconds._

At the interval that `scan-stats` sets, Autoscan prints this information:

- Scans processed
- Scans remaining

## Targets

Targets are the final destination of Scans.
The processor gives Scans to the targets, one batch at a time.

Autoscan supports these targets:

- Plex
- Emby
- Jellyfin
- Autoscan

### Plex

Autoscan replaces the default behavior of Plex that updates the Plex library automatically.
Thus, turn off the `Update my library automatically` feature in Plex.

You can set up one or more Plex targets in the configuration:

```yaml
targets:
  plex:
    - url: https://plex.domain.tld # URL of your Plex server
      token: XXXX # Plex API Token
      rewrite:
        - from: /mnt/unionfs/Media/ # local file system
          to: /data/ # path accessible by the Plex docker container (if applicable)
```

Take note of these fields in the configuration:

- URL. The URL can point to the Docker container directly, to localhost, or to a reverse proxy in front of Plex.
- Token. Autoscan needs a Plex API Token to make requests for you. [This article](https://support.plex.tv/articles/204059436-finding-an-authentication-token-x-plex-token/) tells you how to find your token.
- Rewrite. Plex can run in a Docker container instead of on the host OS. Autoscan can also run in a Docker container. In both cases, you must rewrite the paths. For more information, read the [rewriting section](#rewriting-paths).

### Emby

Emby picks up file changes better than Plex does without extra configuration.
You can still use Autoscan for better performance.

You can set up one or more Emby targets in the configuration:

```yaml
targets:
  emby:
    - url: https://emby.domain.tld # URL of your Emby server
      token: XXXX # Emby API Token
      rewrite:
        - from: /mnt/unionfs/Media/ # local file system
          to: /data/ # path accessible by the Emby docker container (if applicable)
```

- URL. The URL can point to the Docker container directly, to localhost, or to a reverse proxy in front of Emby.
- Token. Autoscan needs an Emby API Token to make requests for you. [This article](https://github.com/MediaBrowser/Emby/wiki/Api-Key-Authentication) tells you how to get a token. \
  _The article is a bit out of date._
- Rewrite. Emby can run in a Docker container instead of on the host OS. Autoscan can also run in a Docker container. In both cases, you must rewrite the paths. For more information, read the [rewriting section](#rewriting-paths).

### Jellyfin

Jellyfin picks up file changes better than Plex does without extra configuration.
You can still use Autoscan for better performance.

You can set up one or more Jellyfin targets in the configuration:

```yaml
targets:
  jellyfin:
    - url: https://jellyfin.domain.tld # URL of your Jellyfin server
      token: XXXX # Jellyfin API Token
      rewrite:
        - from: /mnt/unionfs/Media/ # local file system
          to: /data/ # path accessible by the Jellyfin docker container (if applicable)
```

- URL. The URL can point to the Docker container directly, to localhost, or to a reverse proxy in front of Jellyfin.
- Token. We need a Jellyfin API Token to make requests on your behalf. To create one, open the Jellyfin dashboard, go to API Keys, and add a key. \
  Autoscan sends the token in the `Authorization: MediaBrowser Token="<token>"` header. Jellyfin 10.8 and later accept this header. Newer Jellyfin releases reject the legacy `X-Emby-Token` header by default. \
  If the URL contains `user:password@` for a reverse proxy with Basic authentication, autoscan uses the `Authorization` header for the proxy. In that case, autoscan sends the token in the `ApiKey` query parameter. Autoscan removes the token and the password from its logs. \
  If your proxy logs request URLs, configure it to remove the `ApiKey` parameter from its access log. Otherwise, the access log contains the token. To keep the token out of URLs, use a URL without `user:password@`, such as a direct internal Jellyfin URL.
- Rewrite. Jellyfin can run in a Docker container instead of on the host OS. Autoscan can also run in a Docker container. In both cases, you must rewrite the paths. For more information, read the [rewriting section](#rewriting-paths).

### Autoscan

You can also send scan requests to other instances of Autoscan.

```yaml
targets:
  autoscan:
    - url: https://autoscan.domain.tld # URL of Autoscan
      username: XXXX # Username for remote autoscan instance
      password: XXXX # Password for remote autoscan instance
      rewrite:
        - from: /mnt/unionfs/Media/ # local file system
          to: /mnt/nfs/Media/ # path accessible by the remote autoscan instance (if applicable)
```

## Full config file

This example combines the examples from the [triggers](#triggers), [processor](#processor), and [targets](#targets) sections.
A full configuration file can look like this:

```yaml
# <- processor ->

# override the minimum age to 30 minutes:
minimum-age: 30m

# set multiple anchor files
anchors:
  - /mnt/unionfs/drive1.anchor
  - /mnt/unionfs/drive2.anchor

# <- triggers ->

# Protect your webhooks with authentication
authentication:
  username: hello there
  password: general kenobi

# port for Autoscan webhooks to listen on
port: 3030

triggers:
  lidarr:
    - name: lidarr # /triggers/lidarr
      priority: 1

  radarr:
    - name: radarr # /triggers/radarr
      priority: 2
    - name: radarr4k # /triggers/radarr4k
      priority: 5

  readarr:
    - name: readarr # /triggers/readarr
      priority: 1

  sonarr:
    - name: sonarr-docker # /triggers/sonarr-docker
      priority: 2

      # Rewrite the path from within the container
      # to your local filesystem.
      rewrite:
        - from: /tv/
          to: /mnt/unionfs/Media/TV/

# <- targets ->

targets:
  plex:
    - url: https://plex.domain.tld # URL of your Plex server
      token: XXXX # Plex API Token
      rewrite:
        - from: /mnt/unionfs/Media/ # local file system
          to: /data/ # path accessible by the Plex docker container (if applicable)

  emby:
    - url: https://emby.domain.tld # URL of your Emby server
      token: XXXX # Emby API Token
      rewrite:
        - from: /mnt/unionfs/Media/ # local file system
          to: /data/ # path accessible by the Emby docker container (if applicable)
```

## Other configuration options

```yaml
# Specify the port to listen on (3030 is the default when not specified)
port: 3030
```

```yaml
# Specify the host interface(s) to listen on (0.0.0.0 is the default when not specified)
host:
  - 127.0.0.1
  - 172.19.185.13
  - 192.168.0.1:5959
```

Autoscan uses the value of `port` for each host entry that has no port.
You need the `host` field only to listen on more than one interface.

## Other installation options

### Docker

Autoscan has a Docker image on [Docker Hub](https://hub.docker.com/r/l3uddz/autoscan).

Autoscan needs access to all files that pass between the triggers and the targets. \
_Mount the source directory into the container. For many users, this directory is `/mnt/unionfs`._

Make sure that these files are available in the Autoscan container.
The paths can differ between the triggers, Autoscan, and the targets.
In that case, you MUST use [rewrite rules](#rewriting-paths).
You can set these rules for each trigger and each target in the configuration file.

#### Version Tags

The Autoscan Docker image has different versions, which are available as tags.
The `latest` tag usually has the latest stable version.
The other tags are under development.
Use them with caution.

|  Tag   | Description                                        |
| :----: | -------------------------------------------------- |
| latest | Latest stable version from a tagged GitHub release |
| master | Most recent GitHub master commit                   |

#### Usage

```bash
docker run \
  --name=autoscan \
  --user=1000:1001 \
  -p 3030:3030 \
  -v "/opt/autoscan:/config" \
  -v "/mnt/unionfs:/mnt/unionfs:ro" \
  --restart=unless-stopped \
  -d l3uddz/autoscan
```

#### Parameters

The Autoscan Docker image supports these parameters.

|         Parameter         | Function                                                                       |
| :-----------------------: | ------------------------------------------------------------------------------ |
|      `-p 3030:3030`       | The port that the Autoscan webhook triggers use                                |
|   `--user 1000:1000`      | Run the Autoscan binary as an existing user and group on the host              |
| `-e AUTOSCAN_VERBOSITY=0` | The logging verbosity level of Autoscan (0 = info, 1 = debug, 2 = trace)       |
|       `-v /config`        | The configuration file and the database file of Autoscan                       |

The Autoscan configuration file `config.yml` can refer to other volumes.
You must also mount each of these volumes in the container.

### Unraid

This repository has an Unraid container template in [`unraid/autoscan.xml`](unraid/autoscan.xml).
The template sets the image, port 3030, the `/config` folder, a read-only `/media` folder, and the log verbosity.

The image has no PUID or PGID variables.
The template runs Autoscan as user 99 and group 100 (`nobody:users`) with `--user=99:100` in Extra Parameters.

To install Autoscan from the template, do these steps:

1. Open a terminal on the Unraid server.
2. Download the template:

   ```bash
   wget -O /boot/config/plugins/dockerMan/templates-user/my-autoscan.xml \
     https://raw.githubusercontent.com/l3uddz/autoscan/master/unraid/autoscan.xml
   ```

3. Create the configuration folder and give it to user 99 and group 100:

   ```bash
   mkdir -p /mnt/user/appdata/autoscan
   chown 99:100 /mnt/user/appdata/autoscan
   ```

4. Create `/mnt/user/appdata/autoscan/config.yml`. For an example, read the [configuration section](#configuration).
5. In the Unraid web interface, go to Docker and select Add Container.
6. In the Template list, select `autoscan`.
7. Change the Media path to the share that holds your media, then select Apply.

If `config.yml` does not exist, Autoscan stops at start with the message `Failed opening config`.

In `config.yml`, use the paths inside the Autoscan container, for example `/media/TV`.
If Sonarr, Plex, or another container sees a different path for the same files, add [rewrite rules](#rewriting-paths).
