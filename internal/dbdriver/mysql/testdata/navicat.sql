/*
 Navicat Premium Data Transfer

 Source Server         : local
 Source Server Type    : MySQL
 Source Server Version : 80036
 Source Host           : 127.0.0.1:3306
 Source Schema         : shop

 Target Server Type    : MySQL
 Target Server Version : 80036
 File Encoding         : 65001

 Date: 16/11/2026 10:00:00
*/

SET NAMES utf8mb4;
SET FOREIGN_KEY_CHECKS = 0;

-- ----------------------------
-- Table structure for customers
-- ----------------------------
DROP TABLE IF EXISTS `customers`;
CREATE TABLE `customers`  (
  `id` int NOT NULL AUTO_INCREMENT,
  `name` varchar(100) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL DEFAULT 'n/a; none' COMMENT '姓名；含分号;',
  `visits` int NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`) USING BTREE
) ENGINE = InnoDB AUTO_INCREMENT = 3 CHARACTER SET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci ROW_FORMAT = Dynamic;

-- ----------------------------
-- Records of customers
-- ----------------------------
INSERT INTO `customers` VALUES (1, 'O\'Brien; \"Jr\"', 0);
INSERT INTO `customers` VALUES (2, '中文；分号; \\ 反斜杠', 5);

-- ----------------------------
-- View structure for v_customers
-- ----------------------------
DROP VIEW IF EXISTS `v_customers`;
CREATE ALGORITHM = UNDEFINED SQL SECURITY DEFINER VIEW `v_customers` AS select `customers`.`id` AS `id`,`customers`.`name` AS `name` from `customers`;

-- ----------------------------
-- Procedure structure for add_customer
-- ----------------------------
DROP PROCEDURE IF EXISTS `add_customer`;
delimiter ;;
CREATE PROCEDURE `add_customer`(IN p_name VARCHAR(100))
BEGIN
  # a hash comment; inside the body
  INSERT INTO customers (name) VALUES (p_name);
  SELECT LAST_INSERT_ID();
END
;;
delimiter ;

-- ----------------------------
-- Triggers structure for table customers
-- ----------------------------
DROP TRIGGER IF EXISTS `customers_bi`;
delimiter ;;
CREATE TRIGGER `customers_bi` BEFORE INSERT ON `customers` FOR EACH ROW BEGIN
  IF NEW.name = '' THEN SET NEW.name = 'empty;'; END IF;
END
;;
delimiter ;

SET FOREIGN_KEY_CHECKS = 1;
