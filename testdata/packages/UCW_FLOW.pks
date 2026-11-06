create or replace package ucw_flow as
  e_out_of_range exception;
  pragma exception_init(e_out_of_range, -20100);

  procedure clamp (p_value in out number, p_min in number, p_max in number);
  function  fizzbuzz (p_upto in pls_integer) return varchar2;
  function  safe_divide (p_a in number, p_b in number) return number;
  function  run_dynamic (p_literal in number) return number;
end ucw_flow;
/
