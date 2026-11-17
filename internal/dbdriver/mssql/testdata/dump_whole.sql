-- OpsKat SQL Dump
--
-- OpsKat version: 1.16.0
-- Asset: prod-mssql
-- Database: appdb
-- Content: structure and data
-- Options: indexes, foreign keys, views, functions and procedures, triggers, drop statements
-- Date: 2026-11-17 08:00:00 +00:00
--
-- File encoding: UTF-8

-- ----------------------------
-- Drop existing objects
-- ----------------------------
IF EXISTS (SELECT 1 FROM sys.foreign_keys WHERE name = N'FK_orders_customer' AND parent_object_id = OBJECT_ID(N'[dbo].[orders]')) ALTER TABLE [dbo].[orders] DROP CONSTRAINT [FK_orders_customer];
GO
IF EXISTS (SELECT 1 FROM sys.foreign_keys WHERE name = N'FK_customers_parent' AND parent_object_id = OBJECT_ID(N'[sales].[customers]')) ALTER TABLE [sales].[customers] DROP CONSTRAINT [FK_customers_parent];
GO
DROP TRIGGER IF EXISTS [tr_ddl] ON DATABASE;
GO
DROP TRIGGER IF EXISTS [sales].[tr_customers_upd];
GO
DROP PROCEDURE IF EXISTS [sales].[p_rename];
GO
DROP FUNCTION IF EXISTS [dbo].[f_net];
GO
DROP VIEW IF EXISTS [dbo].[v_named];
GO
DROP VIEW IF EXISTS [sales].[v_names];
GO
DROP TABLE IF EXISTS [sales].[customers];
GO
DROP TABLE IF EXISTS [dbo].[orders];
GO

-- ----------------------------
-- Table structure for dbo.orders
-- ----------------------------
CREATE TABLE [dbo].[orders] (
  [id] bigint NOT NULL,
  [customer_id] int NOT NULL,
  [total] AS ([customer_id]*(2)) PERSISTED,
  [payload] varbinary(max) NULL,
  [rv] timestamp NOT NULL,
  [guid] uniqueidentifier NULL CONSTRAINT [DF_orders_guid] DEFAULT (newid()),
  CONSTRAINT [PK_orders] PRIMARY KEY CLUSTERED ([id])
);
GO

-- ----------------------------
-- Records of dbo.orders
-- ----------------------------
INSERT INTO [dbo].[orders] ([id], [customer_id], [payload], [guid]) VALUES ('9007199254740993', '100', 0xff00fe, '6F9619FF-8B86-D011-B42D-00C04FC964FF');
GO

-- ----------------------------
-- Table structure for sales.customers
-- ----------------------------
CREATE TABLE [sales].[customers] (
  [id] int IDENTITY(100, 5) NOT NULL,
  [parent_id] int NULL,
  [name] nvarchar(max) NULL,
  CONSTRAINT [PK_customers] PRIMARY KEY CLUSTERED ([id])
);
GO

-- ----------------------------
-- Records of sales.customers
-- ----------------------------
SET IDENTITY_INSERT [sales].[customers] ON;
GO
INSERT INTO [sales].[customers] ([id], [parent_id], [name]) VALUES ('100', NULL, N'multi
GO
line O''Reilly');
INSERT INTO [sales].[customers] ([id], [parent_id], [name]) VALUES ('105', '100', N'中文');
GO
SET IDENTITY_INSERT [sales].[customers] OFF;
GO

-- ----------------------------
-- Foreign keys
-- ----------------------------
ALTER TABLE [dbo].[orders] WITH CHECK ADD CONSTRAINT [FK_orders_customer] FOREIGN KEY ([customer_id]) REFERENCES [sales].[customers] ([id]) ON DELETE CASCADE;
GO
ALTER TABLE [sales].[customers] WITH NOCHECK ADD CONSTRAINT [FK_customers_parent] FOREIGN KEY ([parent_id]) REFERENCES [sales].[customers] ([id]) ON UPDATE SET NULL NOT FOR REPLICATION;
GO
ALTER TABLE [sales].[customers] NOCHECK CONSTRAINT [FK_customers_parent];
GO

-- ----------------------------
-- View structure for sales.v_names
-- ----------------------------
CREATE VIEW sales.v_names AS SELECT id, name FROM sales.customers
GO

-- ----------------------------
-- View structure for dbo.v_named
-- ----------------------------
CREATE VIEW dbo.v_named AS
SELECT o.id, n.name FROM dbo.orders o JOIN sales.v_names n ON n.id = o.customer_id
GO

-- ----------------------------
-- Function structure for dbo.f_net
-- ----------------------------
CREATE FUNCTION dbo.f_net(@price decimal(18, 4)) RETURNS decimal(18, 4)
AS
BEGIN
  RETURN @price * 0.9;
END
GO

-- ----------------------------
-- Procedure structure for sales.p_rename
-- ----------------------------
CREATE PROCEDURE sales.p_rename @id int, @name nvarchar(100) AS
UPDATE sales.customers SET name = @name WHERE id = @id;
GO

-- ----------------------------
-- Trigger structure for sales.tr_customers_upd
-- ----------------------------
CREATE TRIGGER sales.tr_customers_upd ON sales.customers AFTER UPDATE AS SET NOCOUNT ON;
GO
DISABLE TRIGGER [sales].[tr_customers_upd] ON [sales].[customers];
GO

-- ----------------------------
-- Trigger structure for tr_ddl
-- ----------------------------
CREATE TRIGGER tr_ddl ON DATABASE FOR CREATE_TABLE AS PRINT 'ddl'
GO
DISABLE TRIGGER [tr_ddl] ON DATABASE;
GO

-- ----------------------------
-- Sequence and auto-increment values
-- ----------------------------
DBCC CHECKIDENT (N'[sales].[customers]', RESEED, 110);
GO
