-- OpsKat SQL Dump
--
-- OpsKat version: 1.16.0
-- Asset: prod-pg
-- Database: shop
-- Content: structure only
-- Date: 2026-11-17 08:00:00 +00:00
--
-- File encoding: UTF-8

SET check_function_bodies = false;
CREATE SCHEMA IF NOT EXISTS "app";
CREATE SCHEMA IF NOT EXISTS "lookup";

CREATE TYPE "lookup"."Mood" AS ENUM ('ok');

-- ----------------------------
-- Table structure for app.notes
-- ----------------------------
DROP TABLE IF EXISTS "app"."notes";
CREATE TABLE "app"."notes" (
  "mood" lookup."Mood"
);

RESET check_function_bodies;
