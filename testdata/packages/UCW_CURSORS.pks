create or replace package ucw_cursors as
  function  numbers_csv (p_count in pls_integer) return varchar2;
  procedure init;
  function  call_count return pls_integer;
end ucw_cursors;
/
