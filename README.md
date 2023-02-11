# Octopus WeChat
Octopus WeChat limb.

# Documentation

## Dependency
* SWeChatRobot.dll, wxDriver.dll, wxDriver64.dll from [ljc545w/ComWeChatRobot](https://github.com/ljc545w/ComWeChatRobot/releases)

## Configuration
* configure.yaml
```yaml
limb:
  version: 3.8.1.26 # Required, fake WeChat version
  listen_port: 22222 # Required, port for listening WeChat message
  hook_port: 22223 # Required, WeChat hook port

service:
  addr: ws://10.10.10.10:11111 # Required, ocotpus address
  secret: hello # Reuqired, user defined secret
  ping_interval: 30s # Optional
  send_timeout: 3m # Optional
  sync_delay: 1m # Optional
  sync_interval: 6h # Optional

log:
  level: info
```

## Feature

* Telegram → WeChat
  * [ ] Message types
    * [x] Text
    * [x] Image
    * [x] Sticker
    * [x] Video
    * [ ] Audio
    * [x] File
    * [ ] Mention
    * [ ] Reply
    * [ ] Location
  * [ ] Redaction

* WeChat → Telegram
  * [ ] Message types
    * [x] Text
    * [x] Image
    * [x] Sticker
    * [x] Video
    * [x] Audio
    * [x] File
    * [ ] Mention
    * [x] Reply
    * [x] Location
  * [ ] Chat types
    * [x] Private
    * [x] Group
  * [x] Redaction
