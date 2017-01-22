# [Armor](https://armor.labstack.com) [![License](http://img.shields.io/badge/license-mit-blue.svg?style=flat-square)](https://raw.githubusercontent.com/labstack/armor/master/LICENSE) [![Build Status](http://img.shields.io/travis/labstack/armor.svg?style=flat-square)](https://travis-ci.org/labstack/armor) [![Join the chat at https://gitter.im/labstack/armor](https://img.shields.io/badge/gitter-join%20chat-brightgreen.svg?style=flat-square)](https://gitter.im/labstack/armor) [![Twitter](https://img.shields.io/badge/twitter-@labstack-55acee.svg?style=flat-square)](https://twitter.com/labstack)

## What can it do today?

- Serve HTTP/2
- Automatically install TLS certificates from https://letsencrypt.org
- Proxy HTTP and WebSocket requests
- Define virtual hosts with path level routing
- Graceful shutdown
- Limit request body
- Serve static files
- Log requests
- Gzip response
- Cross-origin Resource Sharing (CORS)
- Security
  - XSSProtection
  - ContentTypeNosniff
  - ContentSecurityPolicy
  - HTTP Strict Transport Security (HSTS)
- Add / Remove trailing slash from the URL with option to redirect
- Redirect requests
 - http to https
 - http to https www
 - http to https non www
 - non www to www
 - www to non www

Most of the functionality is implemented via `Plugin` interface which makes writing
a custom plugin super easy.

## [Get Started](https://armor.labstack.com/guide)

## What's on the roadmap?

- [x] Website
- [ ] Code coverage
- [ ] Test cases
