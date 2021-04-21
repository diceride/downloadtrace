# go-google-cloud-storage-proxy

Simple Google Cloud Storage proxy for App Engine.<br>
Ensures a client can download a file *only once*.

Example

```txt
https://<PROJECT_ID>.<REGION_ID>.r.appspot.com/?file=<FILENAME>
```

## Getting started

### Building the source code

Building the source code

```sh
$ bazel build //...
```

### Deploying

```sh
$ gcloud app deploy
```