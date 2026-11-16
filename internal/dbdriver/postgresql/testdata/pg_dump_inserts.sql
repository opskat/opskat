--
-- PostgreSQL database dump
--

-- Dumped from database version 16.4
-- Dumped by pg_dump version 16.4

SET statement_timeout = 0;
SET lock_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET client_min_messages = warning;

--
-- Name: add_note(text); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.add_note(p_body text) RETURNS integer
    LANGUAGE plpgsql
    AS $$
DECLARE
  new_id integer;
BEGIN
  -- semicolons; everywhere
  INSERT INTO public.notes (body) VALUES (p_body) RETURNING id INTO new_id;
  EXECUTE $q$SELECT 'it''s; quoted'$q$;
  RETURN new_id;
END;
$$;


--
-- Name: notes_touch(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.notes_touch() RETURNS trigger
    LANGUAGE plpgsql
    AS $function$
BEGIN
  NEW.body := NEW.body || $$; touched$$;
  RETURN NEW;
END
$function$;


--
-- Name: double_it(integer); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.double_it(x integer) RETURNS integer
    LANGUAGE sql
    BEGIN ATOMIC
 SELECT CASE WHEN (x > 0) THEN (x * 2) ELSE 0 END AS "case";
END;


SET default_table_access_method = heap;

--
-- Name: notes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.notes (
    id integer NOT NULL,
    body text DEFAULT 'a;b'::text,
    "Mixed;Name" text
);


CREATE SEQUENCE public.notes_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.notes_id_seq OWNED BY public.notes.id;

ALTER TABLE ONLY public.notes ALTER COLUMN id SET DEFAULT nextval('public.notes_id_seq'::regclass);

--
-- Data for Name: notes; Type: TABLE DATA; Schema: public; Owner: -
--

INSERT INTO public.notes VALUES (1, 'it''s; fine -- not a comment', NULL);
INSERT INTO public.notes VALUES (2, E'back\\slash \'quoted\'; done', 'x');
/* a block /* nested; */ comment; */
DO $$
BEGIN
  RAISE NOTICE 'done; %', $x$nested; tag$x$;
END
$$;

SELECT pg_catalog.setval('public.notes_id_seq', 2, true);

CREATE TRIGGER notes_touch BEFORE INSERT ON public.notes FOR EACH ROW EXECUTE FUNCTION public.notes_touch();

--
-- PostgreSQL database dump complete
--

