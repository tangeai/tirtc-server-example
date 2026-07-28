# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is an open-source example collection built on top of the [tange.ai TiRTC (IoT-RTC)](https://tange.ai) lightweight real-time audio/video SDK. It provides non-intrusive upper-layer integrations for IoT embedded device communication scenarios, without modifying the underlying SDK kernel.

## Repository Structure

- `thing-connect/` — IoT device binding + WeChat VoIP + AI voice conversation system. Five Go HTTP servers (`device-server` :9001, `user-server` :9002, `voip-server` :9003, `ai-server` :9004, `call-server` :9005 — local dev deploy ports; repo's `config.yaml.example` defaults are :8081-8085) + Python device simulator, backed by MySQL + Redis + MQTT.
- `docs/` — Integration guide and supplementary documentation.

See `thing-connect/CLAUDE.md` and `thing-connect/README.md` for full details.
