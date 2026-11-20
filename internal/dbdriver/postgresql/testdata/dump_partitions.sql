-- OpsKat SQL Dump
--
-- OpsKat version: 1.16.0
-- Asset: prod-pg
-- Database: shop
-- Content: structure and data
-- Options: indexes, foreign keys, drop statements
-- Date: 2026-11-17 08:00:00 +00:00
--
-- File encoding: UTF-8

SET check_function_bodies = false;

-- ----------------------------
-- Drop existing objects
-- ----------------------------
ALTER TABLE IF EXISTS "public"."readings" DROP CONSTRAINT IF EXISTS "readings_base_fk";
DROP TABLE IF EXISTS "public"."r_default";
DROP TABLE IF EXISTS "public"."r_2026_eu";
DROP TABLE IF EXISTS "public"."r_2026";
DROP TABLE IF EXISTS "public"."readings";
DROP TABLE IF EXISTS "public"."a_child";
DROP TABLE IF EXISTS "public"."base";

-- ----------------------------
-- Table structure for public.base
-- ----------------------------
CREATE SEQUENCE "public"."base_id_seq" AS integer START WITH 1 INCREMENT BY 1 MINVALUE 1 MAXVALUE 2147483647;

CREATE TABLE "public"."base" (
  "id" integer NOT NULL DEFAULT nextval('public.base_id_seq'::regclass),
  "name" text,
  CONSTRAINT "base_pkey" PRIMARY KEY (id)
);

ALTER SEQUENCE "public"."base_id_seq" OWNED BY "public"."base"."id";

-- ----------------------------
-- Records of public.base
-- ----------------------------
INSERT INTO "public"."base" ("id", "name") VALUES ('1', 'parent row');

-- ----------------------------
-- Table structure for public.a_child
-- ----------------------------
CREATE TABLE "public"."a_child" (
  "extra" integer
) INHERITS ("public"."base");

-- ----------------------------
-- Records of public.a_child
-- ----------------------------
INSERT INTO "public"."a_child" ("id", "name", "extra") VALUES ('2', 'child row', '7');

-- ----------------------------
-- Table structure for public.readings
-- ----------------------------
CREATE TABLE "public"."readings" (
  "id" integer NOT NULL,
  "created" date NOT NULL,
  "region" text NOT NULL,
  CONSTRAINT "readings_pkey" PRIMARY KEY (id, created, region)
) PARTITION BY RANGE (created);

CREATE INDEX readings_region ON public.readings USING btree (region);

-- ----------------------------
-- Table structure for public.r_2026
-- ----------------------------
CREATE TABLE "public"."r_2026" PARTITION OF "public"."readings" FOR VALUES FROM ('2026-01-01') TO ('2027-01-01') PARTITION BY LIST (region);

CREATE INDEX r_2026_only ON public.r_2026 USING btree (id);

-- ----------------------------
-- Table structure for public.r_2026_eu
-- ----------------------------
CREATE TABLE "public"."r_2026_eu" PARTITION OF "public"."r_2026" FOR VALUES IN ('eu');

-- ----------------------------
-- Records of public.r_2026_eu
-- ----------------------------
INSERT INTO "public"."r_2026_eu" ("id", "created", "region") VALUES ('1', '2026-03-01', 'eu');

-- ----------------------------
-- Table structure for public.r_default
-- ----------------------------
CREATE TABLE "public"."r_default" PARTITION OF "public"."readings" DEFAULT;

-- ----------------------------
-- Records of public.r_default
-- ----------------------------
INSERT INTO "public"."r_default" ("id", "created", "region") VALUES ('2', '2030-01-01', 'us');

-- ----------------------------
-- Foreign keys
-- ----------------------------
ALTER TABLE "public"."readings" ADD CONSTRAINT "readings_base_fk" FOREIGN KEY (id) REFERENCES base(id);

-- ----------------------------
-- Sequence and auto-increment values
-- ----------------------------
SELECT pg_catalog.setval(pg_catalog.pg_get_serial_sequence('"public"."base"', 'id'), 2, true);

RESET check_function_bodies;
