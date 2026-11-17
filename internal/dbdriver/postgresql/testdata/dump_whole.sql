-- OpsKat SQL Dump
--
-- OpsKat version: 1.16.0
-- Asset: prod-pg
-- Database: shop
-- Content: structure and data
-- Date: 2026-11-17 08:00:00 +00:00
--
-- File encoding: UTF-8

SET check_function_bodies = false;
CREATE SCHEMA IF NOT EXISTS "app";

CREATE TYPE "public"."kind" AS ENUM ('new', 'it''s');

-- ----------------------------
-- Table structure for app.notes
-- ----------------------------
DROP TABLE IF EXISTS "app"."notes";
CREATE SEQUENCE "app"."notes_id_seq" AS integer START WITH 1 INCREMENT BY 1 MINVALUE 1 MAXVALUE 2147483647;

CREATE TABLE "app"."notes" (
  "id" integer NOT NULL DEFAULT nextval('app.notes_id_seq'::regclass),
  "item_id" integer,
  CONSTRAINT "notes_pkey" PRIMARY KEY (id)
);

ALTER SEQUENCE "app"."notes_id_seq" OWNED BY "app"."notes"."id";

-- ----------------------------
-- Records of app.notes
-- ----------------------------
INSERT INTO "app"."notes" ("id", "item_id") VALUES ('1', '7');
INSERT INTO "app"."notes" ("id", "item_id") VALUES ('3', NULL);

-- ----------------------------
-- Table structure for public.items
-- ----------------------------
DROP TABLE IF EXISTS "public"."items";
CREATE TABLE "public"."items" (
  "id" integer NOT NULL GENERATED ALWAYS AS IDENTITY (START WITH 1 INCREMENT BY 1 MINVALUE 1 MAXVALUE 2147483647),
  "kind" public.kind,
  "doubled" integer GENERATED ALWAYS AS ((id * 2)) STORED,
  "parent_id" integer,
  CONSTRAINT "items_pkey" PRIMARY KEY (id)
);

-- ----------------------------
-- Records of public.items
-- ----------------------------
INSERT INTO "public"."items" ("id", "kind", "parent_id") OVERRIDING SYSTEM VALUE VALUES ('7', 'it''s', NULL);
INSERT INTO "public"."items" ("id", "kind", "parent_id") OVERRIDING SYSTEM VALUE VALUES ('9007199254740993', 'new', '7');

-- ----------------------------
-- Foreign keys
-- ----------------------------
ALTER TABLE "app"."notes" ADD CONSTRAINT "notes_item_fk" FOREIGN KEY (item_id) REFERENCES items(id) ON DELETE CASCADE;
ALTER TABLE "public"."items" ADD CONSTRAINT "items_parent_fk" FOREIGN KEY (parent_id) REFERENCES items(id);

-- ----------------------------
-- View structure for public.v_b
-- ----------------------------
DROP VIEW IF EXISTS "public"."v_b";
CREATE VIEW "public"."v_b" AS SELECT id
   FROM items;

-- ----------------------------
-- View structure for public.v_a
-- ----------------------------
DROP VIEW IF EXISTS "public"."v_a";
CREATE VIEW "public"."v_a" AS SELECT id
   FROM v_b;

-- ----------------------------
-- Function structure for public.touch
-- ----------------------------
DROP FUNCTION IF EXISTS "public"."touch"(n integer);
DROP FUNCTION IF EXISTS "public"."touch"(t text);
CREATE OR REPLACE FUNCTION public.touch(n integer)
 RETURNS integer
 LANGUAGE sql
AS $function$SELECT n$function$;
CREATE OR REPLACE FUNCTION public.touch(t text)
 RETURNS text
 LANGUAGE sql
AS $function$SELECT t$function$;

-- ----------------------------
-- Procedure structure for app.cleanup
-- ----------------------------
DROP PROCEDURE IF EXISTS "app"."cleanup"();
CREATE OR REPLACE PROCEDURE app.cleanup()
 LANGUAGE sql
AS $procedure$DELETE FROM app.notes$procedure$;

-- ----------------------------
-- Trigger structure for public.items_touch
-- ----------------------------
DROP TRIGGER IF EXISTS "items_touch" ON "public"."items";
CREATE TRIGGER items_touch BEFORE INSERT ON items FOR EACH ROW EXECUTE FUNCTION touch_items();

-- ----------------------------
-- Sequence and auto-increment values
-- ----------------------------
SELECT pg_catalog.setval(pg_catalog.pg_get_serial_sequence('"app"."notes"', 'id'), 3, true);
SELECT pg_catalog.setval(pg_catalog.pg_get_serial_sequence('"public"."items"', 'id'), 9007199254740993, true);

RESET check_function_bodies;
