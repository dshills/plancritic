# Implementation Plan: User Authentication

## 1. Overview

Add user authentication to the API. We will keep the project dependency-free
and use only the standard library.

## 2. Database Schema

Create a users table with the following columns:
- id (INT, primary key, auto increment)
- email (VARCHAR(255), unique)
- password_hash (VARCHAR(255))
- created_on (DATETIME)

## 3. API Endpoints

### POST /api/register
Accepts email and password, creates a new user.

### POST /api/login
Accepts email and password, returns a JWT token.

## 4. Dependencies

Add github.com/golang-jwt/jwt for token generation.
Add golang.org/x/crypto/bcrypt for password hashing.

## 5. Testing

Write tests for the authentication flow. Make it robust and production-ready.

## 6. Deployment

Deploy to production and optimize later.
