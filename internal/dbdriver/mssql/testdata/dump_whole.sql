-- OpsKat SQL Dump
--
-- OpsKat version: 1.16.0
-- Asset: prod-mssql
-- Database: appdb
-- Content: structure and data
-- Date: 2026-11-17 08:00:00 +00:00
--
-- File encoding: UTF-8

-- ----------------------------
-- Table structure for dbo.orders
-- ----------------------------
DROP TABLE IF EXISTS [dbo].[orders];
GO
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
DROP TABLE IF EXISTS [sales].[customers];
GO
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
DROP VIEW IF EXISTS [sales].[v_names];
GO
CREATE VIEW sales.v_names AS SELECT id, name FROM sales.customers
GO

-- ----------------------------
-- View structure for dbo.v_named
-- ----------------------------
DROP VIEW IF EXISTS [dbo].[v_named];
GO
CREATE VIEW dbo.v_named AS
SELECT o.id, n.name FROM dbo.orders o JOIN sales.v_names n ON n.id = o.customer_id
GO

-- ----------------------------
-- Function structure for dbo.f_net
-- ----------------------------
DROP FUNCTION IF EXISTS [dbo].[f_net];
GO
CREATE FUNCTION dbo.f_net(@price decimal(18, 4)) RETURNS decimal(18, 4)
AS
BEGIN
  RETURN @price * 0.9;
END
GO

-- ----------------------------
-- Procedure structure for sales.p_rename
-- ----------------------------
DROP PROCEDURE IF EXISTS [sales].[p_rename];
GO
CREATE PROCEDURE sales.p_rename @id int, @name nvarchar(100) AS
UPDATE sales.customers SET name = @name WHERE id = @id;
GO

-- ----------------------------
-- Trigger structure for sales.tr_customers_upd
-- ----------------------------
DROP TRIGGER IF EXISTS [sales].[tr_customers_upd];
GO
CREATE TRIGGER sales.tr_customers_upd ON sales.customers AFTER UPDATE AS SET NOCOUNT ON;
GO
DISABLE TRIGGER [sales].[tr_customers_upd] ON [sales].[customers];
GO

-- ----------------------------
-- Trigger structure for tr_ddl
-- ----------------------------
DROP TRIGGER IF EXISTS [tr_ddl] ON DATABASE;
GO
CREATE TRIGGER tr_ddl ON DATABASE FOR CREATE_TABLE AS PRINT 'ddl'
GO
DISABLE TRIGGER [tr_ddl] ON DATABASE;
GO

-- ----------------------------
-- Sequence and auto-increment values
-- ----------------------------
DBCC CHECKIDENT (N'[sales].[customers]', RESEED, 110);
GO
